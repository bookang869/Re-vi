package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/alerts"
	"github.com/bookang869/Re-vi/gateway/internal/github"
	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

// TestRun_DispatchesPublishedAlert publishes one alert and waits for a
// matching repository_dispatch call. The durable "dispatcher" consumer
// legitimately redelivers the whole stream backlog (that's correct restart
// behavior), so other tests' leftover messages on the same NATS instance
// may arrive too — this scans every request received rather than assuming
// the first (or most recent) one is ours.
func TestRun_DispatchesPublishedAlert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q, err := queue.Connect(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Skipf("no local NATS reachable, skipping: %v", err)
	}
	defer q.Close()

	var mu sync.Mutex
	var gotBodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		gotBodies = append(gotBodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	gh := github.NewClient("test-token", "acme", "widgets")
	gh.BaseURL = srv.URL

	consumeCtx, err := Run(context.Background(), q, gh, "PR_REVIEW")
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	defer consumeCtx.Stop()

	a := alerts.Alert{
		AlertID:      fmt.Sprintf("dispatch-test-%d", time.Now().UnixNano()),
		Status:       "firing",
		ServiceName:  "payment-processor",
		TraceID:      "abc",
		ErrorSummary: "boom",
		LogContext:   "line1\nline2",
	}
	payload, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal alert: %v", err)
	}
	if _, err := q.JS.Publish(context.Background(), queue.AlertSubject, payload); err != nil {
		t.Fatalf("publish alert: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		body, cp := findByAlertID(snapshot(&mu, &gotBodies), a.AlertID)
		if cp != nil {
			if body["event_type"] != eventType {
				t.Errorf("event_type = %v, want %v", body["event_type"], eventType)
			}
			if cp["mode"] != "PR_REVIEW" {
				t.Errorf("mode = %v, want PR_REVIEW", cp["mode"])
			}
			if cp["trace_id"] != a.TraceID {
				t.Errorf("trace_id = %v, want %v", cp["trace_id"], a.TraceID)
			}
			if cp["error_summary"] != a.ErrorSummary {
				t.Errorf("error_summary = %v, want %v", cp["error_summary"], a.ErrorSummary)
			}
			if cp["log_context"] != a.LogContext {
				t.Errorf("log_context = %v, want %v", cp["log_context"], a.LogContext)
			}
			return
		}
		select {
		case <-deadline:
			bodies := snapshot(&mu, &gotBodies)
			t.Fatalf("timed out waiting for repository_dispatch matching alert_id=%s; saw %d request(s): %v", a.AlertID, len(bodies), bodies)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func snapshot(mu *sync.Mutex, bodies *[]map[string]any) []map[string]any {
	mu.Lock()
	defer mu.Unlock()
	snap := make([]map[string]any, len(*bodies))
	copy(snap, *bodies)
	return snap
}

// findByAlertID returns the full request body and its client_payload for
// the request whose alert_id matches wantAlertID, or (nil, nil) if none of
// the received requests matched yet.
func findByAlertID(bodies []map[string]any, wantAlertID string) (map[string]any, map[string]any) {
	for _, body := range bodies {
		cp, ok := body["client_payload"].(map[string]any)
		if !ok {
			continue
		}
		if cp["alert_id"] == wantAlertID {
			return body, cp
		}
	}
	return nil, nil
}
