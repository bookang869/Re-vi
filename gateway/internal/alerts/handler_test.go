package alerts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
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

func doRequest(t *testing.T, q *queue.Queue, s *store.Store, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/alerts", strings.NewReader(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	NewHandler(testSecret, q, nil, "PR_REVIEW", s)(rec, req)
	return rec
}

func TestHandler_BadAuth(t *testing.T) {
	cases := []string{"", "Bearer wrong", "Basic dGVzdA==", "Bearer "}
	for _, auth := range cases {
		rec := doRequest(t, nil, nil, `{}`, auth)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: got %d, want 401", auth, rec.Code)
		}
	}
}

func TestHandler_MalformedJSON(t *testing.T) {
	rec := doRequest(t, nil, nil, `not json`, "Bearer "+testSecret)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_MissingRequiredField(t *testing.T) {
	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"X"},"annotations":{},"fingerprint":"f1"}]}`
	rec := doRequest(t, nil, nil, body, "Bearer "+testSecret)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_AllResolved(t *testing.T) {
	body := `{"status":"resolved","alerts":[{"status":"resolved","labels":{"alertname":"X"},"fingerprint":"f1"}]}`
	rec := doRequest(t, nil, nil, body, "Bearer "+testSecret)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}

func TestHandler_ValidFiringAlert(t *testing.T) {
	q := testQueue(t)
	s := testStore(t)
	// unique per test run so repeated runs against a persistent local NATS
	// don't collide with a still-held lock from a previous run
	fingerprint := fmt.Sprintf("f-%d", time.Now().UnixNano())
	alertID := "ServiceErrorRateHigh-" + fingerprint
	body := fmt.Sprintf(`{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ServiceErrorRateHigh","service":"payment-processor","trace_id":"abc"},"annotations":{"summary":"boom-%s"},"startsAt":"2026-07-20T19:30:00Z","fingerprint":"%s"}]}`, fingerprint, fingerprint)

	rec := doRequest(t, q, s, body, "Bearer "+testSecret)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}

	var gotService, gotMode string
	if err := s.DB().QueryRow(`SELECT service_name, revi_mode FROM runs WHERE alert_id = ?`, alertID).Scan(&gotService, &gotMode); err != nil {
		t.Fatalf("run row not written for %s: %v", alertID, err)
	}
	if gotService != "payment-processor" || gotMode != "PR_REVIEW" {
		t.Errorf("got service=%q mode=%q, want payment-processor/PR_REVIEW", gotService, gotMode)
	}

	// duplicate within the lock TTL must be halted, not published again
	rec2 := doRequest(t, q, s, body, "Bearer "+testSecret)
	if rec2.Code != http.StatusOK {
		t.Errorf("duplicate alert: got %d, want 200", rec2.Code)
	}
}
