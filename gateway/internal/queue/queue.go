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

	// Test* give `go test` its own stream, subject, and KV buckets,
	// separate from StreamName/AlertSubject/LocksBucket/DigestBucket.
	// Without this, tests publishing onto the production stream get
	// picked up and acted on for real by a live gateway process also
	// consuming that stream (each durable consumer name is an
	// independent cursor over the whole stream, so renaming the
	// consumer alone would not stop the live process from seeing test
	// messages too) — see ConnectTest.
	TestStreamName   = "ALERTS_TRIAGE_TEST"
	TestAlertSubject = "alerts.triage.test"
	TestLocksBucket  = "locks-test"
	TestDigestBucket = "digest-test"

	// lockTTL is fixed, not refreshed while a repair is in progress —
	// an accepted risk (TRD Sec 6), not a bug to fix here.
	lockTTL = 30 * time.Minute
)

type Queue struct {
	NC     *nats.Conn
	JS     jetstream.JetStream
	Locks  jetstream.KeyValue
	Digest jetstream.KeyValue

	StreamName   string
	AlertSubject string
}

// Connect dials NATS and ensures the production stream and KV buckets
// exist, creating them on first run and reusing them on every run after.
func Connect(ctx context.Context, url string) (*Queue, error) {
	return connect(ctx, url, StreamName, AlertSubject, LocksBucket, DigestBucket)
}

// ConnectTest is Connect's isolated counterpart for `go test`: it dials the
// same NATS server but binds to TestStreamName/TestAlertSubject and
// test-only KV buckets, so a live gateway process consuming StreamName
// never sees (and never real-dispatches) anything a test publishes. Every
// test that talks to a local NATS instance must use this, not Connect.
func ConnectTest(ctx context.Context, url string) (*Queue, error) {
	return connect(ctx, url, TestStreamName, TestAlertSubject, TestLocksBucket, TestDigestBucket)
}

func connect(ctx context.Context, url, streamName, alertSubject, locksBucket, digestBucket string) (*Queue, error) {
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
		Name:     streamName,
		Subjects: []string{alertSubject},
	}); err != nil {
		nc.Close()
		return nil, fmt.Errorf("create stream %s: %w", streamName, err)
	}

	locks, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: locksBucket,
		TTL:    lockTTL,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create locks bucket: %w", err)
	}

	digest, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: digestBucket,
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create digest bucket: %w", err)
	}

	return &Queue{NC: nc, JS: js, Locks: locks, Digest: digest, StreamName: streamName, AlertSubject: alertSubject}, nil
}

func (q *Queue) Close() {
	q.NC.Close()
}
