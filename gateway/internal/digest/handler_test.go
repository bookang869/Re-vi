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

func doRequest(t *testing.T, q *queue.Queue, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/digest/entry", strings.NewReader(body))
	if sig != "" {
		req.Header.Set("X-Revi-Signature", sig)
	}
	rec := httptest.NewRecorder()
	NewHandler(testSecret, q)(rec, req)
	return rec
}

func TestHandler_BadSignature(t *testing.T) {
	body := `{"alert_id":"a1","digest_date":"2026-07-20"}`
	cases := []string{"", "sha256=deadbeef", sign(body) + "x"}
	for _, sig := range cases {
		rec := doRequest(t, nil, body, sig)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("sig=%q: got %d, want 401", sig, rec.Code)
		}
	}
}

func TestHandler_MalformedBody(t *testing.T) {
	rec := doRequest(t, nil, "not json", sign("not json"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_MissingRequiredField(t *testing.T) {
	body := `{"revi_mode":"AUTONOMOUS","outcome":"MERGED","summary":"fixed it"}`
	rec := doRequest(t, nil, body, sign(body))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestHandler_ValidEntry(t *testing.T) {
	q := testQueue(t)
	digestDate := fmt.Sprintf("test-%d", time.Now().UnixNano())
	body, err := json.Marshal(entry{
		AlertID:    "alert-1",
		ReviMode:   "AUTONOMOUS",
		Outcome:    "MERGED",
		Summary:    "fixed nil pointer dereference",
		DigestDate: digestDate,
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, q, string(body), sign(string(body)))
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
}
