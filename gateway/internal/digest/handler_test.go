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
