package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

const testToken = "test-admin-token"

// testQueue connects to the shared test KV bucket and resets the toggle key
// to "unset" both before and after the test, since the bucket is a real
// NATS server (persists across test runs), not reset per-test the way an
// in-memory fixture would be.
func testQueue(t *testing.T) *queue.Queue {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q, err := queue.ConnectTest(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Skipf("no local NATS reachable, skipping: %v", err)
	}
	q.Config.Delete(ctx, queue.RepairEnabledKey)
	t.Cleanup(func() {
		q.Config.Delete(context.Background(), queue.RepairEnabledKey)
		q.Close()
	})
	return q
}

func TestHandlers_BadAuth(t *testing.T) {
	q := testQueue(t)
	cases := []string{"", "Bearer wrong", "Basic dGVzdA==", "Bearer "}
	for _, auth := range cases {
		getReq := httptest.NewRequest(http.MethodGet, "/v1/admin/repair-enabled", nil)
		if auth != "" {
			getReq.Header.Set("Authorization", auth)
		}
		getRec := httptest.NewRecorder()
		NewGetHandler(testToken, q)(getRec, getReq)
		if getRec.Code != http.StatusUnauthorized {
			t.Errorf("GET auth=%q: got %d, want 401", auth, getRec.Code)
		}

		setReq := httptest.NewRequest(http.MethodPost, "/v1/admin/repair-enabled", strings.NewReader(`{"enabled":false}`))
		if auth != "" {
			setReq.Header.Set("Authorization", auth)
		}
		setRec := httptest.NewRecorder()
		NewSetHandler(testToken, q)(setRec, setReq)
		if setRec.Code != http.StatusUnauthorized {
			t.Errorf("POST auth=%q: got %d, want 401", auth, setRec.Code)
		}
	}
}

func TestHandlers_DefaultEnabled(t *testing.T) {
	q := testQueue(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/repair-enabled", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	NewGetHandler(testToken, q)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Enabled {
		t.Errorf("enabled = %v, want true (missing key defaults to enabled)", resp.Enabled)
	}
}

func TestHandlers_SetThenGet(t *testing.T) {
	q := testQueue(t)

	setReq := httptest.NewRequest(http.MethodPost, "/v1/admin/repair-enabled", strings.NewReader(`{"enabled":false}`))
	setReq.Header.Set("Authorization", "Bearer "+testToken)
	setRec := httptest.NewRecorder()
	NewSetHandler(testToken, q)(setRec, setReq)
	if setRec.Code != http.StatusOK {
		t.Fatalf("POST: got %d, want 200", setRec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/admin/repair-enabled", nil)
	getReq.Header.Set("Authorization", "Bearer "+testToken)
	getRec := httptest.NewRecorder()
	NewGetHandler(testToken, q)(getRec, getReq)

	var resp statusResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Enabled {
		t.Errorf("enabled = %v, want false after POST {enabled:false}", resp.Enabled)
	}
}
