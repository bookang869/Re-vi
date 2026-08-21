package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testFixture() Fixture {
	return Fixture{
		FaultID:          "go-nil-deref-01",
		FaultType:        "runtime exception",
		LanguageRuntime:  "go",
		FixtureApp:       "fixture-app-go",
		BaseRef:          "fault/go-nil-deref-01-base-v2",
		ServiceName:      "fixture-app-go",
		ErrorSummary:     "panic: nil pointer",
		ExpectedBehavior: "GET /summarize returns HTTP 200",
	}
}

func TestDispatchAlert_SendsExpectedPayloadAndAuth(t *testing.T) {
	var gotPayload webhookPayload
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newGatewayClient(srv.URL, "topsecret")
	alertID, err := c.dispatchAlert(context.Background(), testFixture(), "bench-1", 2, "t2-1234")
	if err != nil {
		t.Fatalf("dispatchAlert: %v", err)
	}
	if alertID != "go-nil-deref-01-t2-1234" {
		t.Fatalf("unexpected alert_id: %s", alertID)
	}
	if gotAuth != "Bearer topsecret" {
		t.Fatalf("unexpected Authorization header: %s", gotAuth)
	}
	if len(gotPayload.Alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(gotPayload.Alerts))
	}
	a := gotPayload.Alerts[0]
	if a.Labels["fault_id"] != "go-nil-deref-01" || a.Labels["ref"] != "fault/go-nil-deref-01-base-v2" ||
		a.Labels["merge_target"] != "benchmark/go-nil-deref-01" || a.Labels["experiment_id"] != "bench-1" {
		t.Fatalf("unexpected labels: %+v", a.Labels)
	}
	if a.Fingerprint != "t2-1234" {
		t.Fatalf("unexpected fingerprint: %s", a.Fingerprint)
	}
}

func TestDispatchAlert_AcceptedStatusIsSuccess(t *testing.T) {
	// gateway/internal/alerts/handler.go returns 202, not 200, whenever it
	// actually publishes an alert -- 200 is reserved for "every alert in
	// the batch hit an active flap lock, nothing published." A dispatch
	// client that only accepts 200 would treat every real, successful
	// dispatch as a failure (found running the first live 7-fault batch).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := newGatewayClient(srv.URL, "s")
	if _, err := c.dispatchAlert(context.Background(), testFixture(), "bench-1", 1, "t1-1"); err != nil {
		t.Fatalf("dispatchAlert should treat 202 as success, got error: %v", err)
	}
}

func TestDispatchAlert_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newGatewayClient(srv.URL, "wrong")
	if _, err := c.dispatchAlert(context.Background(), testFixture(), "bench-1", 1, "t1-1"); err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}

func TestGetRun_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newGatewayClient(srv.URL, "s")
	status, found, err := c.getRun(context.Background(), "some-alert")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || status != nil {
		t.Fatalf("expected not-found, got found=%v status=%+v", found, status)
	}
}

func TestGetRun_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(runStatus{AlertID: "a", Outcome: strPtr("MERGED")})
	}))
	defer srv.Close()

	c := newGatewayClient(srv.URL, "s")
	status, found, err := c.getRun(context.Background(), "a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || status == nil || status.Outcome == nil || *status.Outcome != "MERGED" {
		t.Fatalf("unexpected result: found=%v status=%+v", found, status)
	}
}

func strPtr(s string) *string { return &s }
