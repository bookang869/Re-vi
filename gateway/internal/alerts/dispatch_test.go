package alerts

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

func TestPublish_DuplicateWithinTTLProducesOnePublishOneDigestMarker(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()

	a := Alert{
		AlertID:      fmt.Sprintf("dup-test-%d", time.Now().UnixNano()),
		Status:       "firing",
		ServiceName:  "payment-processor",
		TraceID:      "abc",
		ErrorSummary: fmt.Sprintf("boom-%d", time.Now().UnixNano()), // unique per run
	}

	streamBefore, err := q.JS.Stream(ctx, queue.StreamName)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	msgsBefore, err := streamBefore.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}

	published1, err := Publish(ctx, q, "PR_REVIEW", a)
	if err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if !published1 {
		t.Fatal("first Publish should have acquired the lock and published")
	}

	published2, err := Publish(ctx, q, "PR_REVIEW", a)
	if err != nil {
		t.Fatalf("second Publish: %v", err)
	}
	if published2 {
		t.Fatal("second Publish (duplicate within TTL) should have been halted, not published")
	}

	msgsAfter, err := streamBefore.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if got := msgsAfter.State.Msgs - msgsBefore.State.Msgs; got != 1 {
		t.Errorf("stream got %d new message(s), want exactly 1", got)
	}

	digestKey := time.Now().Format("2006-01-02")
	entry, err := q.Digest.Get(ctx, "revi.digest."+digestKey)
	if err != nil {
		t.Fatalf("digest entry not found: %v", err)
	}
	if !strings.Contains(string(entry.Value()), "HEALING LOOP HALTED") {
		t.Errorf("digest entry missing halt marker: %s", entry.Value())
	}
}
