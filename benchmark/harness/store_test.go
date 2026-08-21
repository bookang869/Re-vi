package main

import (
	"testing"
)

func TestStore_AppendAndReload(t *testing.T) {
	dir := t.TempDir()

	s, err := openStore(dir, "bench-1")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	outcome := "MERGED"
	if err := s.Append(TrialRecord{FaultID: "f1", TrialNum: 1, Outcome: &outcome}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append(TrialRecord{FaultID: "f1", TrialNum: 2, HarnessError: "boom"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	reloaded, err := openStore(dir, "bench-1")
	if err != nil {
		t.Fatalf("re-openStore: %v", err)
	}
	recs := reloaded.All()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records after reload, got %d", len(recs))
	}
	if recs[0].Outcome == nil || *recs[0].Outcome != "MERGED" {
		t.Fatalf("unexpected first record: %+v", recs[0])
	}
	if recs[1].HarnessError != "boom" {
		t.Fatalf("unexpected second record: %+v", recs[1])
	}
}

func TestStore_EmptyStoreReturnsNoRecords(t *testing.T) {
	s, err := openStore(t.TempDir(), "bench-empty")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("expected no records for a fresh experiment id")
	}
}
