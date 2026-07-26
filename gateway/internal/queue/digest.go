package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// DigestEntry is one record in a day's digest buffer, compiled into the
// Slack morning summary by the digest cron (TRD Sec 2/4).
type DigestEntry struct {
	AlertID   string `json:"alert_id"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

const maxDigestWriteAttempts = 5

// AppendDigestEntry adds entry to the buffered list under
// revi.digest.{digestDate}. Multiple alerts (or the cron's own callers,
// once 2.7 lands) can write to the same day's key at once, so this retries
// on a lost optimistic-concurrency race rather than clobbering another
// writer's entry.
func (q *Queue) AppendDigestEntry(ctx context.Context, digestDate string, entry DigestEntry) error {
	key := "revi.digest." + digestDate

	for attempt := 0; attempt < maxDigestWriteAttempts; attempt++ {
		var entries []DigestEntry
		var revision uint64

		existing, err := q.Digest.Get(ctx, key)
		switch {
		case errors.Is(err, jetstream.ErrKeyNotFound):
			// first entry of the day
		case err != nil:
			return fmt.Errorf("get digest %s: %w", key, err)
		default:
			if err := json.Unmarshal(existing.Value(), &entries); err != nil {
				return fmt.Errorf("decode digest %s: %w", key, err)
			}
			revision = existing.Revision()
		}

		entries = append(entries, entry)
		data, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("encode digest %s: %w", key, err)
		}

		if revision == 0 {
			_, err = q.Digest.Create(ctx, key, data)
		} else {
			_, err = q.Digest.Update(ctx, key, data, revision)
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("write digest %s: %w", key, err)
		}
		// lost a race with a concurrent writer — refetch and retry
	}
	return fmt.Errorf("append digest %s: too many concurrent write conflicts", key)
}
