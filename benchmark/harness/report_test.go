package main

import "testing"

// TestDedupeLatestPerTrial_RetriedTrialCountsOnce guards against a real bug
// found reconciling go-nil-deref-01's Stage 1 re-run 2026-08-21: its first
// attempt recorded a harness_error (a tag-push bug, since fixed), then the
// retry under the same trial_num recorded a clean MERGED+verified row.
// Store.Append never overwrites, so both rows sat in the same results file
// -- without dedupe, printReport would double-count the fault in total_runs
// and keep reporting a phantom harness/infra error forever, even after the
// retry succeeded.
func TestDedupeLatestPerTrial_RetriedTrialCountsOnce(t *testing.T) {
	outcome := "MERGED"
	records := []TrialRecord{
		{FaultID: "go-nil-deref-01", TrialNum: 1, HarnessError: "reset merge target: exit status 1"},
		{FaultID: "go-nil-deref-01", TrialNum: 1, Outcome: &outcome, VerificationOutcome: "passed"},
		{FaultID: "go-divzero-02", TrialNum: 1, Outcome: &outcome, VerificationOutcome: "passed"},
	}

	got := dedupeLatestPerTrial(records)
	if len(got) != 2 {
		t.Fatalf("dedupeLatestPerTrial returned %d records, want 2 (one per unique fault_id+trial_num): %+v", len(got), got)
	}

	var nilDeref TrialRecord
	found := false
	for _, r := range got {
		if r.FaultID == "go-nil-deref-01" {
			nilDeref = r
			found = true
		}
	}
	if !found {
		t.Fatalf("go-nil-deref-01 missing from deduped records: %+v", got)
	}
	if nilDeref.HarnessError != "" {
		t.Fatalf("dedupeLatestPerTrial kept the stale harness_error record instead of the later successful one: %+v", nilDeref)
	}
	if nilDeref.VerificationOutcome != "passed" {
		t.Fatalf("dedupeLatestPerTrial.VerificationOutcome = %q, want %q", nilDeref.VerificationOutcome, "passed")
	}
}
