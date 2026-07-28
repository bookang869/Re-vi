package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

func TestPublish_RepeatedFlapCollapsesIntoOneDigestEntryWithCount(t *testing.T) {
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

	published1, err := Publish(ctx, q, nil, "PR_REVIEW", a)
	if err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if !published1 {
		t.Fatal("first Publish should have acquired the lock and published")
	}

	// simulate the alert flapping three more times within the lock TTL
	const flapCount = 3
	for i := 0; i < flapCount; i++ {
		published, err := Publish(ctx, q, nil, "PR_REVIEW", a)
		if err != nil {
			t.Fatalf("flap Publish %d: %v", i, err)
		}
		if published {
			t.Fatalf("flap Publish %d: duplicate within TTL should have been halted, not published", i)
		}
	}

	msgsAfter, err := streamBefore.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if got := msgsAfter.State.Msgs - msgsBefore.State.Msgs; got != 1 {
		t.Errorf("stream got %d new message(s), want exactly 1", got)
	}

	digestKey := time.Now().Format("2006-01-02")
	raw, err := q.Digest.Get(ctx, "revi.digest."+digestKey)
	if err != nil {
		t.Fatalf("digest entry not found: %v", err)
	}

	var entries []queue.DigestEntry
	if err := json.Unmarshal(raw.Value(), &entries); err != nil {
		t.Fatalf("decode digest entries: %v", err)
	}

	lockKey := queue.LockKey(a.ServiceName, a.ErrorSummary)
	var found *queue.DigestEntry
	for i := range entries {
		if entries[i].Key == lockKey {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatalf("no digest entry for lock key %s among %d entries", lockKey, len(entries))
	}
	if found.Count != flapCount {
		t.Errorf("Count = %d, want %d (one halt marker per flap, collapsed into one entry)", found.Count, flapCount)
	}
	if !strings.Contains(found.Message, "HEALING LOOP HALTED") {
		t.Errorf("digest entry missing halt marker: %s", found.Message)
	}
}
