// Package digestcron runs the Gateway's third in-process responsibility: a
// goroutine that fires once daily at REVI_DIGEST_TIME, flushes the day's
// buffered NATS KV digest to Slack, and deletes the key only after a
// successful send (TRD Sec 2 step 3).
package digestcron

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
	"github.com/bookang869/Re-vi/gateway/internal/slack"
)

// Run starts the cron loop in the background and returns immediately.
// digestTime is "HH:MM" in local time (REVI_DIGEST_TIME, default "08:00").
// The loop stops when ctx is cancelled.
func Run(ctx context.Context, q *queue.Queue, sc *slack.Client, digestTime string) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(nextFire(time.Now(), digestTime))):
				flush(ctx, q, sc, time.Now().Format("2006-01-02"))
			}
		}
	}()
}

// nextFire returns the next occurrence of digestTime ("HH:MM", local) at or
// after now — today if it hasn't passed yet, tomorrow otherwise. A
// malformed digestTime (already validated at config load in practice) falls
// back to 24h out rather than busy-looping.
func nextFire(now time.Time, digestTime string) time.Time {
	t, err := time.ParseInLocation("15:04", digestTime, now.Location())
	if err != nil {
		return now.Add(24 * time.Hour)
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// flush reads revi.digest.{digestDate}, posts it to Slack, and deletes the
// key — but only on a successful post, so a Slack outage leaves the entries
// buffered for the next attempt instead of losing them.
func flush(ctx context.Context, q *queue.Queue, sc *slack.Client, digestDate string) {
	key := "revi.digest." + digestDate
	kv, err := q.Digest.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return
	}
	if err != nil {
		log.Printf("digestcron: read %s: %v", key, err)
		return
	}

	var entries []queue.DigestEntry
	if err := json.Unmarshal(kv.Value(), &entries); err != nil {
		log.Printf("digestcron: decode %s: %v", key, err)
		return
	}

	if err := sc.PostDigest(ctx, digestDate, entries); err != nil {
		log.Printf("digestcron: slack post failed, leaving %s buffered: %v", key, err)
		return
	}

	if err := q.Digest.Delete(ctx, key); err != nil {
		log.Printf("digestcron: posted but failed to clear %s: %v", key, err)
		return
	}
	log.Printf("digestcron: flushed %d entries from %s to slack", len(entries), key)
}
