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
	summary                  TEXT
);
`

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
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}

	return &Store{db: db}, nil
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
		INSERT INTO runs (alert_id, service_name, revi_mode, received_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(alert_id) DO NOTHING`,
		r.AlertID, r.ServiceName, r.ReviMode, receivedAt)
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
	if err := s.db.QueryRowContext(ctx, `SELECT revi_mode FROM runs WHERE alert_id = ?`, o.AlertID).Scan(&storedMode); err != nil && err != sql.ErrNoRows {
		log.Printf("store: looking up revi_mode for %s failed: %v", o.AlertID, err)
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
	var attempts, inputTokens, outputTokens any
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
			summary = ?
		WHERE alert_id = ?`,
		validatedAt, mergedAt, o.Outcome, o.EscalationReason, attempts,
		o.FailureStage, o.FailureClassification, inputTokens, outputTokens,
		o.Model, estimatedCost, o.Summary, o.AlertID)
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
