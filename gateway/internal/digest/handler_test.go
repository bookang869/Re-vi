package digest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/metrics"
	"github.com/bookang869/Re-vi/gateway/internal/queue"
	"github.com/bookang869/Re-vi/gateway/internal/store"
)

const testSecret = "test-secret"

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// testQueue connects to a real local NATS+JetStream instance and skips the
// test if none is reachable (see queue_test.go for the same pattern).
func testQueue(t *testing.T) *queue.Queue {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q, err := queue.ConnectTest(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Skipf("no local NATS reachable, skipping: %v", err)
	}
	t.Cleanup(q.Close)
	return q
}

func sign(body string) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func doRequest(t *testing.T, q *queue.Queue, s *store.Store, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/digest/entry", strings.NewReader(body))
	if sig != "" {
		req.Header.Set("X-Revi-Signature", sig)
	}
	rec := httptest.NewRecorder()
	NewHandler(testSecret, q, s)(rec, req)
	return rec
}

func TestHandler_BadSignature(t *testing.T) {
	body := `{"alert_id":"a1","digest_date":"2026-07-20"}`
	cases := []string{"", "sha256=deadbeef", sign(body) + "x"}
	for _, sig := range cases {
		rec := doRequest(t, nil, nil, body, sig)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("sig=%q: got %d, want 401", sig, rec.Code)
		}
	}
}

func TestHandler_MalformedBody(t *testing.T) {
	rec := doRequest(t, nil, nil, "not json", sign("not json"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_MissingRequiredField(t *testing.T) {
	body := `{"revi_mode":"AUTONOMOUS","outcome":"MERGED","summary":"fixed it"}`
	rec := doRequest(t, nil, nil, body, sign(body))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_ValidEntry(t *testing.T) {
	q := testQueue(t)
	s := testStore(t)
	s.InsertRun(context.Background(), store.RunStart{AlertID: "alert-1", ServiceName: "payment-processor", ReviMode: "AUTONOMOUS"})

	digestDate := fmt.Sprintf("test-%d", time.Now().UnixNano())
	body, err := json.Marshal(entry{
		AlertID:       "alert-1",
		ReviMode:      "AUTONOMOUS",
		Outcome:       "MERGED",
		Summary:       "fixed nil pointer dereference",
		DigestDate:    digestDate,
		Validated:     true,
		Merged:        true,
		Attempts:      intPtr(1),
		InputTokens:   intPtr(1200),
		OutputTokens:  intPtr(340),
		Model:         "claude-sonnet-5",
		EstimatedCost: floatPtr(0.021),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, q, s, string(body), sign(string(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}

	kv, err := q.Digest.Get(context.Background(), "revi.digest."+digestDate)
	if err != nil {
		t.Fatalf("digest key not written: %v", err)
	}
	var entries []queue.DigestEntry
	if err := json.Unmarshal(kv.Value(), &entries); err != nil {
		t.Fatalf("decode digest entries: %v", err)
	}
	if len(entries) != 1 || entries[0].AlertID != "alert-1" {
		t.Errorf("got entries %+v, want one entry for alert-1", entries)
	}

	var validatedAt, mergedAt, outcome, model *string
	var attempts *int
	if err := s.DB().QueryRow(`SELECT validated_at, merged_at, outcome, model, attempts FROM runs WHERE alert_id = ?`, "alert-1").
		Scan(&validatedAt, &mergedAt, &outcome, &model, &attempts); err != nil {
		t.Fatalf("run row not found: %v", err)
	}
	if validatedAt == nil || mergedAt == nil {
		t.Error("expected validated_at and merged_at to be set for a merged AUTONOMOUS run")
	}
	if outcome == nil || *outcome != "MERGED" || model == nil || *model != "claude-sonnet-5" || attempts == nil || *attempts != 1 {
		t.Errorf("unexpected stored fields: outcome=%v model=%v attempts=%v", outcome, model, attempts)
	}
}

// TestHandler_ReleasesLockOnCompletion covers release-on-completion
// (2026-08-19): a run's final report must take its flap lock down
// immediately rather than leaving it to the TTL backstop, so a genuinely
// new alert for the same (service, error) isn't blocked from starting a
// fresh repair long after this run actually finished.
func TestHandler_ReleasesLockOnCompletion(t *testing.T) {
	q := testQueue(t)
	s := testStore(t)
	alertID := fmt.Sprintf("release-test-%d", time.Now().UnixNano())
	serviceName := "payment-processor"
	errorSummary := "nil pointer in Summarize"
	s.InsertRun(context.Background(), store.RunStart{AlertID: alertID, ServiceName: serviceName, ReviMode: "PR_REVIEW"})

	alreadyLocked, err := q.TryLock(context.Background(), serviceName, errorSummary, []byte("held"))
	if err != nil {
		t.Fatalf("TryLock() error: %v", err)
	}
	if alreadyLocked {
		t.Fatal("lock should not already be held at test start")
	}

	digestDate := fmt.Sprintf("test-%d", time.Now().UnixNano())
	body, err := json.Marshal(entry{
		AlertID:      alertID,
		ReviMode:     "PR_REVIEW",
		Outcome:      "PR_READY",
		Summary:      "fixed it",
		DigestDate:   digestDate,
		Validated:    true,
		ServiceName:  serviceName,
		ErrorSummary: errorSummary,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, q, s, string(body), sign(string(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}

	if _, err := q.Locks.Get(context.Background(), queue.LockKey(serviceName, errorSummary)); err == nil {
		t.Error("lock should have been released after the final report, but is still held")
	}
}

// smokeFailuresCount scrapes the current revi_synthetic_smoke_failures_total
// value off metrics.Handler -- there's no exported reader, only the
// Prometheus text exposition endpoint.
func smokeFailuresCount(t *testing.T) uint64 {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	var count uint64
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if n, err := fmt.Sscanf(line, "revi_synthetic_smoke_failures_total %d", &count); err == nil && n == 1 {
			return count
		}
	}
	t.Fatal("revi_synthetic_smoke_failures_total not found in /metrics output")
	return 0
}

func TestHandler_IncrementsSmokeFailureOnBootStage(t *testing.T) {
	q := testQueue(t)
	s := testStore(t)
	before := smokeFailuresCount(t)

	body, err := json.Marshal(entry{
		AlertID:      fmt.Sprintf("smoke-boot-test-%d", time.Now().UnixNano()),
		ReviMode:     "AUTONOMOUS",
		Outcome:      "APP_BOOT_FAILURE",
		Summary:      "app crashed on boot",
		DigestDate:   fmt.Sprintf("test-%d", time.Now().UnixNano()),
		FailureStage: "boot",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, q, s, string(body), sign(string(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}
	if got := smokeFailuresCount(t); got != before+1 {
		t.Errorf("revi_synthetic_smoke_failures_total = %d, want %d (failure_stage=boot must increment it)", got, before+1)
	}
}

func TestHandler_DoesNotIncrementSmokeFailureOnOtherStages(t *testing.T) {
	q := testQueue(t)
	s := testStore(t)
	before := smokeFailuresCount(t)

	body, err := json.Marshal(entry{
		AlertID:      fmt.Sprintf("smoke-regression-test-%d", time.Now().UnixNano()),
		ReviMode:     "AUTONOMOUS",
		Outcome:      "REGRESSION",
		Summary:      "existing test suite failed after patch",
		DigestDate:   fmt.Sprintf("test-%d", time.Now().UnixNano()),
		FailureStage: "regression_test",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, q, s, string(body), sign(string(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}
	if got := smokeFailuresCount(t); got != before {
		t.Errorf("revi_synthetic_smoke_failures_total = %d, want unchanged %d (failure_stage=regression_test must not increment it)", got, before)
	}
}
