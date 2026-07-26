// Package queue wraps the NATS JetStream connection: the alerts.triage
// stream and the locks/digest KV buckets (TRD Sec 2/4).
package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	StreamName   = "ALERTS_TRIAGE"
	AlertSubject = "alerts.triage"

	LocksBucket  = "locks"
	DigestBucket = "digest"

	// lockTTL is fixed, not refreshed while a repair is in progress —
	// an accepted risk (TRD Sec 6), not a bug to fix here.
	lockTTL = 30 * time.Minute
)

type Queue struct {
	NC     *nats.Conn
	JS     jetstream.JetStream
	Locks  jetstream.KeyValue
	Digest jetstream.KeyValue
}

// Connect dials NATS and ensures the stream and KV buckets exist,
// creating them on first run and reusing them on every run after.
func Connect(ctx context.Context, url string) (*Queue, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream: %w", err)
	}

	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{AlertSubject},
	}); err != nil {
		nc.Close()
		return nil, fmt.Errorf("create stream %s: %w", StreamName, err)
	}

	locks, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: LocksBucket,
		TTL:    lockTTL,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create locks bucket: %w", err)
	}

	digest, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: DigestBucket,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create digest bucket: %w", err)
	}

	return &Queue{NC: nc, JS: js, Locks: locks, Digest: digest}, nil
}

func (q *Queue) Close() {
	q.NC.Close()
}
