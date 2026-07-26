package queue

import (
	"context"
	"testing"
	"time"
)

// requires a real NATS+JetStream instance (docker-compose's `nats` service,
// or `docker run -p 4222:4222 nats:2.10-alpine -js`); skips otherwise.
func TestConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	q, err := Connect(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Skipf("no local NATS reachable, skipping: %v", err)
	}
	defer q.Close()

	status := q.JS
	if status == nil {
		t.Fatal("JetStream context is nil")
	}
	if _, err := q.JS.Stream(ctx, StreamName); err != nil {
		t.Errorf("stream %s not created: %v", StreamName, err)
	}
	if _, err := q.JS.KeyValue(ctx, LocksBucket); err != nil {
		t.Errorf("locks bucket not created: %v", err)
	}
	if _, err := q.JS.KeyValue(ctx, DigestBucket); err != nil {
		t.Errorf("digest bucket not created: %v", err)
	}

	// idempotent: connecting again must reuse, not error
	q2, err := Connect(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Fatalf("second Connect() failed: %v", err)
	}
	q2.Close()
}
