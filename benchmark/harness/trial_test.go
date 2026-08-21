package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollUntilOutcome_WaitsThroughNotFoundThenPendingThenDone(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		switch {
		case n <= 2:
			// Row not written yet -- expected early-poll state, not an error.
			w.WriteHeader(http.StatusNotFound)
		case n == 3:
			// Row exists but the digest report hasn't landed yet.
			json.NewEncoder(w).Encode(runStatus{AlertID: "a"})
		default:
			outcome := "MERGED"
			json.NewEncoder(w).Encode(runStatus{AlertID: "a", Outcome: &outcome})
		}
	}))
	defer srv.Close()

	gw := newGatewayClient(srv.URL, "s")
	status, err := pollUntilOutcome(context.Background(), gw, "a", 5*time.Millisecond, 50)
	if err != nil {
		t.Fatalf("pollUntilOutcome: %v", err)
	}
	if status.Outcome == nil || *status.Outcome != "MERGED" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if calls < 4 {
		t.Fatalf("expected at least 4 polls, got %d", calls)
	}
}

func TestPollUntilOutcome_TimesOutIfOutcomeNeverAppears(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	gw := newGatewayClient(srv.URL, "s")
	_, err := pollUntilOutcome(context.Background(), gw, "a", 1*time.Millisecond, 3)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestRunTrial_DispatchFailureRecordsHarnessErrorNotVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := Config{PollInterval: time.Millisecond, MaxPolls: 1, TargetClone: t.TempDir()}
	gw := newGatewayClient(srv.URL, "s")
	store, err := openStore(t.TempDir(), "bench-test")
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	f := Fixture{FaultID: "f1", FaultType: "runtime exception", LanguageRuntime: "go", FixtureApp: "fixture-app-go", BaseRef: "t", ServiceName: "s", ErrorSummary: "e", ExpectedBehavior: "b"}

	rec := runTrial(context.Background(), cfg, gw, store, f, "bench-test", 1)
	if rec.HarnessError == "" {
		t.Fatal("expected a harness error when the reset-merge-target git step fails (no real clone configured)")
	}
	if rec.VerificationOutcome != "" {
		t.Fatalf("a harness-level failure must not carry a verification verdict, got %q", rec.VerificationOutcome)
	}
	// Confirm it was actually persisted, not just returned.
	all := store.All()
	if len(all) != 1 || all[0].HarnessError == "" {
		t.Fatalf("expected the failed trial to be persisted with its harness_error, got %+v", all)
	}
}
