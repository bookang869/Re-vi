package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bookang869/Re-vi/gateway/internal/store"
)

const testSecret = "test-secret"

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func doRequest(t *testing.T, s *store.Store, alertID, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+alertID, nil)
	req.SetPathValue("alert_id", alertID)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	NewHandler(testSecret, s)(rec, req)
	return rec
}

func TestHandler_BadAuth(t *testing.T) {
	cases := []string{"", "Bearer wrong", "Basic dGVzdA==", "Bearer "}
	for _, auth := range cases {
		rec := doRequest(t, nil, "a1", auth)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: got %d, want 401", auth, rec.Code)
		}
	}
}

func TestHandler_UnknownAlertID(t *testing.T) {
	s := testStore(t)
	rec := doRequest(t, s, "ghost", "Bearer "+testSecret)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestHandler_PendingRun(t *testing.T) {
	// A run row exists (POST /v1/alerts already published it) but no digest
	// entry has arrived yet -- the poll loop must be able to tell this
	// apart from "row not written yet" (404 above).
	s := testStore(t)
	s.InsertRun(context.Background(), store.RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS"})

	rec := doRequest(t, s, "a1", "Bearer "+testSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AlertID != "a1" {
		t.Errorf("alert_id = %q, want a1", resp.AlertID)
	}
	if resp.Outcome != nil || resp.ValidatedAt != nil || resp.MergedAt != nil || resp.Attempts != nil {
		t.Errorf("expected all outcome fields null while pending, got %+v", resp)
	}
}

func TestHandler_CompletedRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.InsertRun(ctx, store.RunStart{AlertID: "a1", ServiceName: "svc", ReviMode: "AUTONOMOUS"})
	s.RecordOutcome(ctx, store.RunOutcome{
		AlertID:   "a1",
		Validated: true,
		Merged:    true,
		Outcome:   "MERGED",
		Attempts:  intPtr(2),
	})

	rec := doRequest(t, s, "a1", "Bearer "+testSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}

	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Outcome == nil || *resp.Outcome != "MERGED" {
		t.Errorf("outcome = %v, want MERGED", resp.Outcome)
	}
	if resp.ValidatedAt == nil || resp.MergedAt == nil {
		t.Errorf("expected validated_at/merged_at set, got %+v", resp)
	}
	if resp.Attempts == nil || *resp.Attempts != 2 {
		t.Errorf("attempts = %v, want 2", resp.Attempts)
	}
}

func intPtr(v int) *int { return &v }
