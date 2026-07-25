package alerts

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecret = "test-secret"

func doRequest(t *testing.T, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/alerts", strings.NewReader(body))
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	NewHandler(testSecret)(rec, req)
	return rec
}

func TestHandler_BadAuth(t *testing.T) {
	cases := []string{"", "Bearer wrong", "Basic dGVzdA==", "Bearer "}
	for _, auth := range cases {
		rec := doRequest(t, `{}`, auth)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: got %d, want 401", auth, rec.Code)
		}
	}
}

func TestHandler_MalformedJSON(t *testing.T) {
	rec := doRequest(t, `not json`, "Bearer "+testSecret)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_MissingRequiredField(t *testing.T) {
	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"X"},"annotations":{},"fingerprint":"f1"}]}`
	rec := doRequest(t, body, "Bearer "+testSecret)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_ValidFiringAlert(t *testing.T) {
	body := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ServiceErrorRateHigh","service":"payment-processor","trace_id":"abc"},"annotations":{"summary":"boom"},"startsAt":"2026-07-20T19:30:00Z","fingerprint":"f1"}]}`
	rec := doRequest(t, body, "Bearer "+testSecret)
	if rec.Code != http.StatusAccepted {
		t.Errorf("got %d, want 202", rec.Code)
	}
}

func TestHandler_AllResolved(t *testing.T) {
	body := `{"status":"resolved","alerts":[{"status":"resolved","labels":{"alertname":"X"},"fingerprint":"f1"}]}`
	rec := doRequest(t, body, "Bearer "+testSecret)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
}
