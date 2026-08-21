package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// TrialRecord is one row of the harness's own result store.
// docs/observability-part-b.md "Locked: verifier isolation" (second
// /grill-me pass): independent_verification_passed lives here, not as a
// Gateway column -- this struct is the "one more field in that same state"
// the design doc describes, joined against the Gateway's own data
// (AlertID/FaultID) rather than written back to it.
type TrialRecord struct {
	ExperimentID    string `json:"experiment_id"`
	FaultID         string `json:"fault_id"`
	FaultType       string `json:"fault_type"`
	LanguageRuntime string `json:"language_runtime"`
	FixtureApp      string `json:"fixture_app"`
	TrialNum        int    `json:"trial_num"`
	AlertID         string `json:"alert_id"`

	DispatchedAt string `json:"dispatched_at"`
	CompletedAt  string `json:"completed_at,omitempty"`

	// Outcome/EscalationReason/Attempts/FailureStage/ValidatedAt/MergedAt
	// mirror GET /v1/runs/{alert_id}'s response fields verbatim -- see
	// gatewayclient.go's runStatus and gateway/internal/runs/handler.go.
	// Pointers so "the Gateway never answered" (nil) is distinguishable
	// from "answered with an empty/false value".
	Outcome          *string `json:"outcome"`
	EscalationReason *string `json:"escalation_reason"`
	Attempts         *int    `json:"attempts"`
	FailureStage     *string `json:"failure_stage"`
	ValidatedAt      *string `json:"validated_at"`
	MergedAt         *string `json:"merged_at"`

	// MergeSHA is resolved by the harness itself (targetrepo.go's
	// mergedCommitSHA), not reported by the Gateway -- only populated when
	// Outcome == "MERGED".
	MergeSHA string `json:"merge_sha,omitempty"`

	// VerificationOutcome is one of verifyPassed/verifyFailed/
	// verifyInconclusive (verify.go), or "not_applicable" when Outcome
	// never reached MERGED -- there is nothing to independently verify if
	// nothing merged, and that must not be conflated with a scored
	// failure (docs/observability-part-b.md's three-way verify.sh
	// contract exists for exactly this reason).
	VerificationOutcome string `json:"verification_outcome,omitempty"`
	VerifierExitCode    *int   `json:"verifier_exit_code,omitempty"`
	VerifierOutput      string `json:"verifier_output,omitempty"`

	// HarnessError is non-empty only for a harness/infrastructure failure
	// (dispatch error, poll timeout, git operation failure) -- distinct
	// from a real Hermes/verifier verdict. Stage 1's gate
	// (docs/observability-part-b.md "Locked: staged rollout") checks this
	// field specifically: a fault that correctly failed its verifier is a
	// valid outcome and must not block Stage 2, but a non-empty
	// HarnessError means the trial itself never ran cleanly and must.
	HarnessError string `json:"harness_error,omitempty"`
}

const verificationNotApplicable = "not_applicable"

// Store is a mutex-protected, file-backed append log for one experiment's
// trial records. Kept deliberately simple (a JSON array rewritten
// atomically) rather than a database: at 96 trials total the whole file
// fits comfortably in memory, and a human should be able to read the
// result of a benchmark run with `cat`, no query tool required.
type Store struct {
	path string
	mu   sync.Mutex
	recs []TrialRecord

	// experimentIDHint is this store's experiment_id, stamped onto every
	// record trial.go/stage.go write through it -- kept on the Store
	// itself so callers don't have to thread the experiment_id through
	// every function signature separately from the store that's already
	// scoped to it.
	experimentIDHint string
}

func openStore(resultsDir, experimentID string) (*Store, error) {
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create results dir %s: %w", resultsDir, err)
	}
	path := filepath.Join(resultsDir, experimentID+".json")

	s := &Store{path: path, experimentIDHint: experimentID}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s.recs); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return s, nil
}

// Append adds one trial record and durably persists the whole file. Called
// once per completed trial (not per poll), so contention is low even with
// 32 faults running concurrently.
func (s *Store) Append(r TrialRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, r)
	return s.saveLocked()
}

// All returns a snapshot copy of every record recorded so far.
func (s *Store) All() []TrialRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TrialRecord, len(s.recs))
	copy(out, s.recs)
	return out
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.recs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, s.path, err)
	}
	return nil
}
