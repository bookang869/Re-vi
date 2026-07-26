package alerts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

const testSecret = "test-secret"

// testQueue connects to a real local NATS+JetStream instance and skips the
// test if none is reachable (see queue_test.go for the same pattern).
func testQueue(t *testing.T) *queue.Queue {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q, err := queue.Connect(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Skipf("no local NATS reachable, skipping: %v", err)
	}
	t.Cleanup(q.Close)
	return q
}

func doRequest(t *testing.T, q *queue.Queue, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/alerts", strings.NewReader(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	NewHandler(testSecret, q, "PR_REVIEW")(rec, req)
	return rec
}

func TestHandler_BadAuth(t *testing.T) {
	cases := []string{"", "Bearer wrong", "Basic dGVzdA==", "Bearer "}
	for _, auth := range cases {
		rec := doRequest(t, nil, `{}`, auth)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: got %d, want 401", auth, rec.Code)
		}
	}
}

func TestHandler_MalformedJSON(t *testing.T) {
	rec := doRequest(t, nil, `not json`, "Bearer "+testSecret)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_MissingRequiredField(t *testing.T) {
	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"X"},"annotations":{},"fingerprint":"f1"}]}`
	rec := doRequest(t, nil, body, "Bearer "+testSecret)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_AllResolved(t *testing.T) {
	body := `{"status":"resolved","alerts":[{"status":"resolved","labels":{"alertname":"X"},"fingerprint":"f1"}]}`
	rec := doRequest(t, nil, body, "Bearer "+testSecret)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}

func TestHandler_ValidFiringAlert(t *testing.T) {
	q := testQueue(t)
	// unique per test run so repeated runs against a persistent local NATS
	// don't collide with a still-held lock from a previous run
	fingerprint := fmt.Sprintf("f-%d", time.Now().UnixNano())
	body := fmt.Sprintf(`{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ServiceErrorRateHigh","service":"payment-processor","trace_id":"abc"},"annotations":{"summary":"boom-%s"},"startsAt":"2026-07-20T19:30:00Z","fingerprint":"%s"}]}`, fingerprint, fingerprint)

	rec := doRequest(t, q, body, "Bearer "+testSecret)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}

	// duplicate within the lock TTL must be halted, not published again
	rec2 := doRequest(t, q, body, "Bearer "+testSecret)
	if rec2.Code != http.StatusOK {
		t.Errorf("duplicate alert: got %d, want 200", rec2.Code)
	}
}
