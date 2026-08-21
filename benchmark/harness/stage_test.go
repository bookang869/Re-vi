package main

import "testing"

func TestStage1Gate_PassesOnlyWhenEveryFaultHasACleanTrial1(t *testing.T) {
	fixtures := []Fixture{{FaultID: "f1"}, {FaultID: "f2"}}

	dir := t.TempDir()
	store, err := openStore(dir, "bench-gate")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}

	// Only f1 has recorded a trial 1 so far -- f2 is missing entirely.
	if err := store.Append(TrialRecord{FaultID: "f1", TrialNum: 1}); err != nil {
		t.Fatal(err)
	}
	if passed, missing := stage1Gate(store, fixtures); passed || len(missing) != 1 {
		t.Fatalf("expected gate to fail on f2 missing, got passed=%v missing=%v", passed, missing)
	}

	// f2 shows up but with a harness error -- gate must still fail.
	if err := store.Append(TrialRecord{FaultID: "f2", TrialNum: 1, HarnessError: "dispatch failed"}); err != nil {
		t.Fatal(err)
	}
	if passed, missing := stage1Gate(store, fixtures); passed || len(missing) != 1 {
		t.Fatalf("expected gate to fail on f2's harness_error, got passed=%v missing=%v", passed, missing)
	}

	// A fault correctly failing its verifier (no harness_error, just a
	// real "failed" verdict) must NOT block the gate -- this is the whole
	// point of the gate being mechanical rather than results-based
	// (docs/observability-part-b.md "Locked: staged rollout").
	fresh, err := openStore(dir, "bench-gate-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Append(TrialRecord{FaultID: "f1", TrialNum: 1, VerificationOutcome: string(verifyFailed)}); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Append(TrialRecord{FaultID: "f2", TrialNum: 1, VerificationOutcome: verificationNotApplicable}); err != nil {
		t.Fatal(err)
	}
	if passed, missing := stage1Gate(fresh, fixtures); !passed || len(missing) != 0 {
		t.Fatalf("expected gate to pass despite a real verifier failure, got passed=%v missing=%v", passed, missing)
	}
}
