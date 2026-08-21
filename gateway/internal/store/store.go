// Package store persists one row per remediation run to an embedded SQLite
// database (docs/observability-part-a.md). It is colocated with the
// Gateway process — no new container, no new service (see that doc for why
// SQLite over NATS KV or a client-server DB).
//
// Writes here are best-effort observability, not the source of truth for
// pipeline behavior (NATS KV locks/digest remain that): a store failure is
// logged and swallowed rather than propagated, the same "never block the
// primary flow" convention internal/victorialogs uses for log enrichment.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/metrics"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	alert_id                TEXT PRIMARY KEY,
	service_name             TEXT NOT NULL,
	revi_mode                TEXT NOT NULL,
	received_at              TEXT NOT NULL,
	validated_at             TEXT,
	merged_at                TEXT,
	outcome                  TEXT,
	escalation_reason        TEXT,
	attempts                 INTEGER,
	failure_stage            TEXT,
	failure_classification   TEXT,
	input_tokens             INTEGER,
	output_tokens            INTEGER,
	model                    TEXT,
	estimated_cost           REAL,
	summary                  TEXT,

	-- Part B (docs/observability-part-b.md): nullable, populated only on
	-- benchmark runs. experiment_id/fault_id/fault_type/language_runtime/
	-- expected_behavior come in on the synthetic alert that starts the run
	-- (same path as service_name); candidate_patch_count/
	-- validation_gate_rejections come in on the runner's outcome report
	-- (same path as attempts/failure_stage). independent_verification_passed
	-- deliberately has no column here: the verifier lives outside the
	-- runner entirely (docs/observability-part-b.md "Locked: verifier
	-- isolation"), so only the harness can ever know it, and only after
	-- this row is already complete -- it lives in the harness's own store
	-- instead, joined by alert_id/fault_id at report time, rather than
	-- adding a benchmark-only write endpoint to a production component.
	experiment_id              TEXT,
	fault_id                   TEXT,
	fault_type                 TEXT,
	language_runtime           TEXT,
	expected_behavior          TEXT,
	candidate_patch_count      INTEGER,
	validation_gate_rejections TEXT
);
`

// additiveColumns are the nullable columns that have been added to the
// schema after the original table was first created. CREATE TABLE IF NOT
// EXISTS (schema, above) only ever fires against a brand-new file — it's a
// silent no-op against a file that already has the runs table under an
// older column set, so a new binary deployed onto an existing database
// would otherwise fail every write that touches these columns until
// someone runs an ALTER TABLE by hand (happened for real 2026-08-20/21
// deploying Part B's columns onto the Oracle VPS's pre-existing revi.db —
// see docs/observability-part-b.md's "First live trial" section).
// migrateColumns (below) closes that gap by diffing this list against
// PRAGMA table_info and adding whatever's missing. The next time a column
// gets added to the schema string, add it here too, in the same commit —
// this list is what actually makes an existing deployment pick it up.
var additiveColumns = []struct {
	name string
	ddl  string
}{
	{"experiment_id", "TEXT"},
	{"fault_id", "TEXT"},
	{"fault_type", "TEXT"},
	{"language_runtime", "TEXT"},
	{"expected_behavior", "TEXT"},
	{"candidate_patch_count", "INTEGER"},
	{"validation_gate_rejections", "TEXT"},
}

type Store struct {
	db *sql.DB
}

// Open creates/migrates the SQLite file at path and returns a Store backed
// by it. SQLite allows only one writer at a time, so the connection pool is
// capped at 1 — this project's write volume (per-run, not per-request)
// never makes that a bottleneck (docs/observability-part-a.md).
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create sqlite schema: %w", err)
	}
	if err := migrateColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}

	return &Store{db: db}, nil
}

// migrateColumns adds any of additiveColumns missing from an existing runs
// table — the retrofit CREATE TABLE IF NOT EXISTS in schema can't do, since
// it's a no-op against a table that already exists under an older column
// set. A no-op itself against a freshly-created table, since schema already
// created every column in one shot.
func migrateColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return fmt.Errorf("read table_info: %w", err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan table_info: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table_info: %w", err)
	}
	rows.Close()

	for _, col := range additiveColumns {
		if existing[col.name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE runs ADD COLUMN %s %s`, col.name, col.ddl)); err != nil {
			return fmt.Errorf("add column %s: %w", col.name, err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for ad-hoc querying — the doc's own
// intended verification path (docs/observability-part-a.md: "confirm real
// data lands correctly by querying the file directly") and for tests in
// other packages to assert what got written.
func (s *Store) DB() *sql.DB {
	return s.db
}

// RunStart is the row written from POST /v1/alerts once Publish() reports a
// genuinely new dispatch (published=true) — repeat Alertmanager
// notifications for an alert still being worked must never reach this.
type RunStart struct {
	AlertID     string
	ServiceName string
	ReviMode    string

	// Benchmark-only (docs/observability-part-b.md); empty on every real
	// run. Set from the synthetic alert's labels/annotations when the
	// harness fires it.
	ExperimentID     string
	FaultID          string
	FaultType        string
	LanguageRuntime  string
	ExpectedBehavior string
}

// InsertRun records a new run. alert_id is the primary key; ON CONFLICT DO
// NOTHING guards against a duplicate insert (e.g. a retried request)
// silently overwriting the true received_at, which would corrupt latency
// numbers (docs/observability-part-a.md).
func (s *Store) InsertRun(ctx context.Context, r RunStart) {
	if s == nil {
		return
	}
	receivedAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (alert_id, service_name, revi_mode, received_at,
			experiment_id, fault_id, fault_type, language_runtime, expected_behavior)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))
		ON CONFLICT(alert_id) DO NOTHING`,
		r.AlertID, r.ServiceName, r.ReviMode, receivedAt,
		r.ExperimentID, r.FaultID, r.FaultType, r.LanguageRuntime, r.ExpectedBehavior)
	if err != nil {
		log.Printf("store: insert run %s failed: %v", r.AlertID, err)
	}
}

// RunOutcome is the update written from POST /v1/digest/entry once the
// runner reports back. Validated/Merged are explicit signals from the
// runner (not inferred from Outcome/EscalationReason's free-form strings)
// so the Gateway can stamp validated_at/merged_at unambiguously at receipt
// time — the runner performs both events synchronously just before
// reporting, so "now" is an accurate proxy (docs/observability-part-a.md).
// There's no ReviMode field here: RecordOutcome checks the AUTONOMOUS
// invariant against the row's own stored revi_mode, not a value the caller
// supplies, so the payload has no way to influence that decision.
//
// Attempts/InputTokens/OutputTokens/EstimatedCost are pointers, not bare
// numbers: the wrapper script doesn't track token/cost data yet (as of
// 2026-08-18, docs/observability-part-a.md), and a literal 0 would read as
// "this run cost nothing" rather than "unknown" — a nil pointer stores SQL
// NULL instead of a fabricated zero.
type RunOutcome struct {
	AlertID string

	Validated bool
	Merged    bool

	Outcome               string
	EscalationReason      string
	Attempts              *int
	FailureStage          string
	FailureClassification string
	InputTokens           *int
	OutputTokens          *int
	Model                 string
	EstimatedCost         *float64
	Summary               string

	// Benchmark-only (docs/observability-part-b.md); empty/nil on every real
	// run. The runner tracks these directly (it's the one running the
	// build/boot/smoke/regression gates), unlike independent_verification_passed
	// which it structurally cannot know. ValidationGateRejections is a
	// small JSON object keyed by gate name, e.g. {"build":1,"regression":2}.
	CandidatePatchCount      *int
	ValidationGateRejections string
}

// RecordOutcome fills in the outcome fields for an existing run. MergedAt
// only ever gets stamped for AUTONOMOUS mode, even if Merged is set on the
// payload — PR_REVIEW never merges synchronously, so a true value there
// would be a runner bug, not a real merge (docs/observability-part-a.md,
// TRD's mode-scoped merge invariant). The AUTONOMOUS check reads the row's
// own stored revi_mode (set once, at insert time, from the Gateway's own
// config) rather than trusting o.ReviMode — the payload's claim about its
// own mode — since a mismatched/spoofed value there should never be able to
// stamp a merge that didn't happen.
func (s *Store) RecordOutcome(ctx context.Context, o RunOutcome) {
	if s == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)

	var storedMode string
	var existingOutcome sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT revi_mode, outcome FROM runs WHERE alert_id = ?`, o.AlertID).Scan(&storedMode, &existingOutcome); err != nil && err != sql.ErrNoRows {
		log.Printf("store: looking up revi_mode/outcome for %s failed: %v", o.AlertID, err)
	}

	// A non-NULL outcome means some earlier call already recorded a real
	// result for this alert_id (outcome is only ever set here, never by
	// InsertRun). A second report for the same alert_id means two
	// independent dispatches happened for what should have been one
	// genuinely new alert — a lock-TTL race, a redelivered NATS message,
	// or (as found 2026-08-19) a manual test seeding a row through the
	// real /v1/alerts path while also running a rehearsal against the same
	// alert_id. Whatever the cause, the first recorded outcome wins; this
	// is a signal to surface loudly, not data to silently merge or let the
	// last write clobber.
	if existingOutcome.Valid {
		log.Printf("store: duplicate digest entry for alert_id %s ignored -- existing outcome=%q, incoming outcome=%q", o.AlertID, existingOutcome.String, o.Outcome)
		metrics.IncDuplicateDigestEntries()
		return
	}

	var validatedAt, mergedAt any
	if o.Validated {
		validatedAt = now
	}
	if o.Merged && storedMode == "AUTONOMOUS" {
		mergedAt = now
	}

	// Pointer fields are converted to `any` explicitly (nil -> SQL NULL,
	// non-nil -> the underlying value) rather than passed as *int/*float64
	// directly, so this doesn't depend on the sqlite driver's own pointer
	// handling.
	var attempts, inputTokens, outputTokens, candidatePatchCount any
	var estimatedCost any
	if o.Attempts != nil {
		attempts = *o.Attempts
	}
	if o.InputTokens != nil {
		inputTokens = *o.InputTokens
	}
	if o.OutputTokens != nil {
		outputTokens = *o.OutputTokens
	}
	if o.EstimatedCost != nil {
		estimatedCost = *o.EstimatedCost
	}
	if o.CandidatePatchCount != nil {
		candidatePatchCount = *o.CandidatePatchCount
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE runs SET
			validated_at = COALESCE(?, validated_at),
			merged_at = COALESCE(?, merged_at),
			outcome = ?,
			escalation_reason = NULLIF(?, ''),
			attempts = ?,
			failure_stage = NULLIF(?, ''),
			failure_classification = NULLIF(?, ''),
			input_tokens = ?,
			output_tokens = ?,
			model = NULLIF(?, ''),
			estimated_cost = ?,
			summary = ?,
			candidate_patch_count = ?,
			validation_gate_rejections = NULLIF(?, '')
		WHERE alert_id = ?`,
		validatedAt, mergedAt, o.Outcome, o.EscalationReason, attempts,
		o.FailureStage, o.FailureClassification, inputTokens, outputTokens,
		o.Model, estimatedCost, o.Summary, candidatePatchCount,
		o.ValidationGateRejections, o.AlertID)
	if err != nil {
		log.Printf("store: record outcome for %s failed: %v", o.AlertID, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Every run should have a row from /v1/alerts first; a miss here
		// means the digest entry arrived for an alert_id the Gateway never
		// saw a published dispatch for. Log for visibility, don't fail the
		// request — the runner's own report is still worth accepting.
		log.Printf("store: digest entry for unknown alert_id %s (no matching run row)", o.AlertID)
	}
}
