package digestcron

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
	"github.com/bookang869/Re-vi/gateway/internal/slack"
)

func TestNextFire(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 28, 8, 30, 0, 0, loc)

	cases := []struct {
		name       string
		digestTime string
		want       time.Time
	}{
		{"later today", "20:00", time.Date(2026, 7, 28, 20, 0, 0, 0, loc)},
		{"already passed", "08:00", time.Date(2026, 7, 29, 8, 0, 0, 0, loc)},
		{"exact now rolls to tomorrow", "08:30", time.Date(2026, 7, 29, 8, 30, 0, 0, loc)},
		{"malformed falls back 24h", "not-a-time", now.Add(24 * time.Hour)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nextFire(now, c.digestTime)
			if !got.Equal(c.want) {
				t.Errorf("nextFire(%v, %q) = %v, want %v", now, c.digestTime, got, c.want)
			}
		})
	}
}

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

func testDigestDate(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-%d", time.Now().UnixNano())
}

func TestFlush_PostsAndClearsKey(t *testing.T) {
	q := testQueue(t)
	digestDate := testDigestDate(t)

	err := q.UpsertDigestEntry(context.Background(), digestDate, "k1", queue.DigestEntry{
		AlertID: "alert-1", Message: "fixed it", LastSeen: "t1",
	})
	if err != nil {
		t.Fatalf("seed digest entry: %v", err)
	}

	var gotBlocks int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBlocks++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	sc := slack.NewClient(srv.URL)

	flush(context.Background(), q, sc, digestDate)

	if gotBlocks != 1 {
		t.Errorf("slack webhook called %d times, want 1", gotBlocks)
	}
	if _, err := q.Digest.Get(context.Background(), "revi.digest."+digestDate); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Errorf("digest key not cleared after successful flush: err=%v", err)
	}
}

func TestFlush_NoEntriesIsNoop(t *testing.T) {
	q := testQueue(t)
	digestDate := testDigestDate(t)

	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	sc := slack.NewClient(srv.URL)

	flush(context.Background(), q, sc, digestDate)

	if called {
		t.Error("slack webhook called with nothing buffered")
	}
}

func TestFlush_SlackFailureLeavesKeyBuffered(t *testing.T) {
	q := testQueue(t)
	digestDate := testDigestDate(t)

	if err := q.UpsertDigestEntry(context.Background(), digestDate, "k1", queue.DigestEntry{AlertID: "alert-1"}); err != nil {
		t.Fatalf("seed digest entry: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	sc := slack.NewClient(srv.URL)

	flush(context.Background(), q, sc, digestDate)

	if _, err := q.Digest.Get(context.Background(), "revi.digest."+digestDate); err != nil {
		t.Errorf("key should remain buffered after a failed slack post: %v", err)
	}
}
