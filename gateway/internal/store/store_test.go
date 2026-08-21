package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type row struct {
	ServiceName           string
	ReviMode              string
	ReceivedAt            string
	ValidatedAt           sql.NullString
	MergedAt              sql.NullString
	Outcome               sql.NullString
	EscalationReason      sql.NullString
	Attempts              sql.NullInt64
	FailureStage          sql.NullString
	FailureClassification sql.NullString
	Model                 sql.NullString
}

func fetch(t *testing.T, s *Store, alertID string) row {
	t.Helper()
	var r row
	err := s.db.QueryRow(`SELECT service_name, revi_mode, received_at, validated_at, merged_at,
		outcome, escalation_reason, attempts, failure_stage, failure_classification, model
		FROM runs WHERE alert_id = ?`, alertID).Scan(
		&r.ServiceName, &r.ReviMode, &r.ReceivedAt, &r.ValidatedAt, &r.MergedAt,
		&r.Outcome, &r.EscalationReason, &r.Attempts, &r.FailureStage, &r.FailureClassification, &r.Model)
	if err != nil {
		t.Fatalf("fetch %s: %v", alertID, err)
	}
	return r
}

func TestInsertRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "payment-processor", ReviMode: "AUTONOMOUS"})

	r := fetch(t, s, "a1")
	if r.ServiceName != "payment-processor" || r.ReviMode != "AUTONOMOUS" || r.ReceivedAt == "" {
		t.Errorf("unexpected row: %+v", r)
	}
	if r.ValidatedAt.Valid || r.MergedAt.Valid {
		t.Errorf("outcome fields should be null on insert: %+v", r)
	}
}

func TestInsertRun_BenchmarkFieldsStayNullOnRealRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// A real run's RunStart never sets the benchmark fields -- they must
	// land as SQL NULL, not empty string, so real runs stay cleanly
	// distinguishable from benchmark ones.
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS"})

	var faultID, faultType sql.NullString
	if err := s.db.QueryRow(`SELECT fault_id, fault_type FROM runs WHERE alert_id = ?`, "a1").
		Scan(&faultID, &faultType); err != nil {
		t.Fatalf("query: %v", err)
	}
	if faultID.Valid || faultType.Valid {
		t.Errorf("expected NULL benchmark fields on a real run, got fault_id=%v fault_type=%v", faultID, faultType)
	}
}

func TestInsertRun_BenchmarkFieldsStored(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.InsertRun(ctx, RunStart{
		AlertID:          "a1",
		ServiceName:      "fixture-app-go",
		ReviMode:         "AUTONOMOUS",
		ExperimentID:     "exp-1",
		FaultID:          "go-nil-deref-01",
		FaultType:        "runtime exception",
		LanguageRuntime:  "go",
		ExpectedBehavior: "handler returns 200 instead of panicking",
	})

	var experimentID, faultID, faultType, languageRuntime, expectedBehavior string
	if err := s.db.QueryRow(`SELECT experiment_id, fault_id, fault_type, language_runtime, expected_behavior
		FROM runs WHERE alert_id = ?`, "a1").
		Scan(&experimentID, &faultID, &faultType, &languageRuntime, &expectedBehavior); err != nil {
		t.Fatalf("query: %v", err)
	}
	if experimentID != "exp-1" || faultID != "go-nil-deref-01" || faultType != "runtime exception" ||
		languageRuntime != "go" || expectedBehavior != "handler returns 200 instead of panicking" {
		t.Errorf("unexpected benchmark fields: experiment_id=%q fault_id=%q fault_type=%q language_runtime=%q expected_behavior=%q",
			experimentID, faultID, faultType, languageRuntime, expectedBehavior)
	}
}

func TestInsertRun_DuplicateDoesNotOverwrite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc-a", ReviMode: "PR_REVIEW"})
	first := fetch(t, s, "a1").ReceivedAt

	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc-b", ReviMode: "AUTONOMOUS"})
	second := fetch(t, s, "a1")

	if second.ReceivedAt != first || second.ServiceName != "svc-a" || second.ReviMode != "PR_REVIEW" {
		t.Errorf("duplicate insert overwrote original row: %+v", second)
	}
}

func TestRecordOutcome_ValidatedOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "PR_REVIEW"})

	s.RecordOutcome(ctx, RunOutcome{
		AlertID:   "a1",
		Validated: true,
		Merged:    false,
		Outcome:   "PR_READY",
	})

	r := fetch(t, s, "a1")
	if !r.ValidatedAt.Valid {
		t.Error("validated_at should be set")
	}
	if r.MergedAt.Valid {
		t.Error("merged_at should stay null for PR_REVIEW")
	}
	if r.Outcome.String != "PR_READY" {
		t.Errorf("outcome = %q, want PR_READY", r.Outcome.String)
	}
}

func TestRecordOutcome_MergedIgnoredOutsideAutonomous(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "PR_REVIEW"})

	// A runner bug sending merged=true under PR_REVIEW must not stamp
	// merged_at — PR_REVIEW never merges synchronously (TRD).
	s.RecordOutcome(ctx, RunOutcome{AlertID: "a1", Merged: true})

	r := fetch(t, s, "a1")
	if r.MergedAt.Valid {
		t.Error("merged_at should not be set for PR_REVIEW even if Merged=true")
	}
}

func TestRecordOutcome_AutonomousMerged(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS"})

	s.RecordOutcome(ctx, RunOutcome{
		AlertID:   "a1",
		Validated: true,
		Merged:    true,
		Outcome:   "MERGED",
		Attempts:  intPtr(2),
		Model:     "claude-sonnet-5",
	})

	r := fetch(t, s, "a1")
	if !r.ValidatedAt.Valid || !r.MergedAt.Valid {
		t.Errorf("expected both timestamps set: %+v", r)
	}
	if r.Attempts.Int64 != 2 || r.Model.String != "claude-sonnet-5" {
		t.Errorf("unexpected fields: %+v", r)
	}
}

func TestRecordOutcome_UnknownCostStaysNullNotZero(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS"})

	// Omitting token/cost fields (nil pointers, matching a runner that
	// doesn't track them yet) must leave the columns NULL, not a literal 0
	// that would misleadingly read as "this run cost nothing."
	s.RecordOutcome(ctx, RunOutcome{AlertID: "a1", Validated: true, Outcome: "MERGED"})

	var inputTokens, outputTokens sql.NullInt64
	var estimatedCost sql.NullFloat64
	if err := s.db.QueryRow(`SELECT input_tokens, output_tokens, estimated_cost FROM runs WHERE alert_id = ?`, "a1").
		Scan(&inputTokens, &outputTokens, &estimatedCost); err != nil {
		t.Fatalf("query: %v", err)
	}
	if inputTokens.Valid || outputTokens.Valid || estimatedCost.Valid {
		t.Errorf("expected NULL cost fields when unset, got input=%v output=%v cost=%v", inputTokens, outputTokens, estimatedCost)
	}
}

func TestRecordOutcome_KnownCostIsStored(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS"})

	s.RecordOutcome(ctx, RunOutcome{
		AlertID:       "a1",
		Validated:     true,
		Outcome:       "MERGED",
		InputTokens:   intPtr(1200),
		OutputTokens:  intPtr(340),
		EstimatedCost: floatPtr(0.021),
	})

	var inputTokens, outputTokens sql.NullInt64
	var estimatedCost sql.NullFloat64
	if err := s.db.QueryRow(`SELECT input_tokens, output_tokens, estimated_cost FROM runs WHERE alert_id = ?`, "a1").
		Scan(&inputTokens, &outputTokens, &estimatedCost); err != nil {
		t.Fatalf("query: %v", err)
	}
	if inputTokens.Int64 != 1200 || outputTokens.Int64 != 340 || estimatedCost.Float64 != 0.021 {
		t.Errorf("unexpected values: input=%v output=%v cost=%v", inputTokens, outputTokens, estimatedCost)
	}
}

func TestRecordOutcome_Escalated(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS"})

	s.RecordOutcome(ctx, RunOutcome{
		AlertID:               "a1",
		Validated:             false,
		Outcome:               "ESCALATED",
		EscalationReason:      "REGRESSION",
		FailureStage:          "regression_test",
		FailureClassification: "remediation_failure",
	})

	r := fetch(t, s, "a1")
	if r.ValidatedAt.Valid || r.MergedAt.Valid {
		t.Errorf("no timestamps should be set for a failed run: %+v", r)
	}
	if r.EscalationReason.String != "REGRESSION" || r.FailureStage.String != "regression_test" || r.FailureClassification.String != "remediation_failure" {
		t.Errorf("unexpected failure fields: %+v", r)
	}
}

func TestRecordOutcome_BenchmarkGateFieldsStored(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS", FaultID: "go-nil-deref-01"})

	s.RecordOutcome(ctx, RunOutcome{
		AlertID:                  "a1",
		Validated:                true,
		Merged:                   true,
		Outcome:                  "MERGED",
		CandidatePatchCount:      intPtr(3),
		ValidationGateRejections: `{"build":1,"regression":1}`,
	})

	var candidatePatchCount sql.NullInt64
	var validationGateRejections sql.NullString
	if err := s.db.QueryRow(`SELECT candidate_patch_count, validation_gate_rejections FROM runs WHERE alert_id = ?`, "a1").
		Scan(&candidatePatchCount, &validationGateRejections); err != nil {
		t.Fatalf("query: %v", err)
	}
	if candidatePatchCount.Int64 != 3 || validationGateRejections.String != `{"build":1,"regression":1}` {
		t.Errorf("unexpected gate fields: candidate_patch_count=%v validation_gate_rejections=%v", candidatePatchCount, validationGateRejections)
	}
}

func TestRecordOutcome_UnknownAlertIsNoop(t *testing.T) {
	s := testStore(t)
	// No InsertRun for "ghost" — must not panic or error, just log.
	s.RecordOutcome(context.Background(), RunOutcome{AlertID: "ghost", Outcome: "MERGED"})
}

func TestRecordOutcome_DuplicateReportRejected(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "PR_REVIEW"})

	s.RecordOutcome(ctx, RunOutcome{
		AlertID:   "a1",
		Validated: true,
		Outcome:   "PR_READY",
		Summary:   "first, correct report",
	})
	first := fetch(t, s, "a1")

	// A second report for the same alert_id — e.g. a redelivered dispatch,
	// or (found 2026-08-19) a real hermes-triage.yml run colliding with a
	// rehearsal against the same alert_id — must not overwrite the first
	// recorded outcome.
	s.RecordOutcome(ctx, RunOutcome{
		AlertID:          "a1",
		Outcome:          "FAILED",
		EscalationReason: "EXHAUSTION",
		Summary:          "second, should be rejected",
	})
	second := fetch(t, s, "a1")

	if second.Outcome.String != first.Outcome.String || second.Outcome.String != "PR_READY" {
		t.Errorf("duplicate report overwrote original outcome: got %+v, want unchanged from %+v", second, first)
	}
	if second.ValidatedAt.String != first.ValidatedAt.String {
		t.Errorf("duplicate report changed validated_at: got %+v, want unchanged from %+v", second, first)
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	s.InsertRun(context.Background(), RunStart{AlertID: "a1"})
	s.RecordOutcome(context.Background(), RunOutcome{AlertID: "a1"})
	if err := s.Close(); err != nil {
		t.Errorf("Close() on nil store: %v", err)
	}
}

// TestOpen_MigratesExistingFileMissingAdditiveColumns reproduces what
// happened for real deploying Part B's schema change onto the Oracle VPS's
// pre-existing revi.db (docs/observability-part-b.md's "First live trial"
// section): a file created under the original (pre-Part-B) column set, then
// reopened by a binary whose schema string now includes additiveColumns.
// CREATE TABLE IF NOT EXISTS alone is a no-op against it; Open() must add
// the missing columns itself.
func TestOpen_MigratesExistingFileMissingAdditiveColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	oldSchemaDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open pre-existing file: %v", err)
	}
	const oldSchema = `
		CREATE TABLE runs (
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
		);`
	if _, err := oldSchemaDB.Exec(oldSchema); err != nil {
		t.Fatalf("create old-schema table: %v", err)
	}
	if _, err := oldSchemaDB.Exec(`INSERT INTO runs (alert_id, service_name, revi_mode, received_at) VALUES ('pre-existing', 'svc', 'PR_REVIEW', 'x')`); err != nil {
		t.Fatalf("seed pre-existing row: %v", err)
	}
	if err := oldSchemaDB.Close(); err != nil {
		t.Fatalf("close pre-existing file: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() against old-schema file: %v", err)
	}
	defer s.Close()

	// The pre-existing row must survive the migration untouched.
	var serviceName string
	if err := s.db.QueryRow(`SELECT service_name FROM runs WHERE alert_id = 'pre-existing'`).Scan(&serviceName); err != nil {
		t.Fatalf("pre-existing row lost after migration: %v", err)
	}
	if serviceName != "svc" {
		t.Errorf("pre-existing row corrupted: service_name = %q, want %q", serviceName, "svc")
	}

	// A write that touches an additive column must now succeed instead of
	// silently failing (the actual production bug).
	s.InsertRun(context.Background(), RunStart{
		AlertID:     "new-after-migration",
		ServiceName: "fixture-app-go",
		ReviMode:    "AUTONOMOUS",
		FaultID:     "go-nil-deref-01",
	})
	var faultID sql.NullString
	if err := s.db.QueryRow(`SELECT fault_id FROM runs WHERE alert_id = 'new-after-migration'`).Scan(&faultID); err != nil {
		t.Fatalf("row with additive column not written: %v", err)
	}
	if !faultID.Valid || faultID.String != "go-nil-deref-01" {
		t.Errorf("fault_id = %+v, want valid \"go-nil-deref-01\"", faultID)
	}
}
