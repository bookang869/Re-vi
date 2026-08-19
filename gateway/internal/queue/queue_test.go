package queue

import (
	"context"
	"fmt"
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
	if _, err := q.JS.KeyValue(ctx, DispatchedBucket); err != nil {
		t.Errorf("dispatched bucket not created: %v", err)
	}

	// idempotent: connecting again must reuse, not error
	q2, err := Connect(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Fatalf("second Connect() failed: %v", err)
	}
	q2.Close()
}

func TestTryMarkDispatched(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q, err := ConnectTest(ctx, "nats://127.0.0.1:4222")
	if err != nil {
		t.Skipf("no local NATS reachable, skipping: %v", err)
	}
	defer q.Close()

	// The dispatched bucket is durable across test runs against the same
	// local NATS instance (24h TTL, not test-run-scoped), so alert_ids must
	// be unique per run — a fixed id like "a1" would collide with a
	// previous run's leftover marker and fail on the second `go test`.
	alert1 := fmt.Sprintf("mark-test-%d-a1", time.Now().UnixNano())
	alert2 := fmt.Sprintf("mark-test-%d-a2", time.Now().UnixNano())

	alreadyDispatched, err := q.TryMarkDispatched(ctx, alert1, []byte("first"))
	if err != nil {
		t.Fatalf("first TryMarkDispatched() error: %v", err)
	}
	if alreadyDispatched {
		t.Fatal("first claim should not report already dispatched")
	}

	// Redelivery of the same alert_id (Gateway restart/slow-ack, or a
	// second alert let through by an expired flap lock) must be detected.
	alreadyDispatched, err = q.TryMarkDispatched(ctx, alert1, []byte("second"))
	if err != nil {
		t.Fatalf("second TryMarkDispatched() error: %v", err)
	}
	if !alreadyDispatched {
		t.Fatal("second claim for the same alert_id should report already dispatched")
	}

	// A different alert_id must be unaffected.
	alreadyDispatched, err = q.TryMarkDispatched(ctx, alert2, []byte("first"))
	if err != nil {
		t.Fatalf("TryMarkDispatched() for alert2 error: %v", err)
	}
	if alreadyDispatched {
		t.Fatal("a different alert_id must not be seen as already dispatched")
	}

	// UnmarkDispatched (the rollback-on-dispatch-failure path) must let a
	// legitimate retry through afterward.
	if err := q.UnmarkDispatched(ctx, alert1); err != nil {
		t.Fatalf("UnmarkDispatched() error: %v", err)
	}
	alreadyDispatched, err = q.TryMarkDispatched(ctx, alert1, []byte("retry after rollback"))
	if err != nil {
		t.Fatalf("TryMarkDispatched() after unmark error: %v", err)
	}
	if alreadyDispatched {
		t.Fatal("claim after UnmarkDispatched should succeed, not report already dispatched")
	}
}
