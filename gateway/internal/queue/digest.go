package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// DigestEntry is one record in a day's digest buffer, compiled into the
// Slack morning summary by the digest cron (TRD Sec 2/4). Key identifies
// the underlying event (e.g. the flap lock key) so repeats collapse into
// one entry with a running Count instead of one line per occurrence.
type DigestEntry struct {
	Key       string `json:"key"`
	AlertID   string `json:"alert_id"`
	Message   string `json:"message"`
	Count     int    `json:"count"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

const maxDigestWriteAttempts = 5

// UpsertDigestEntry records an occurrence of the event identified by key
// under revi.digest.{digestDate}. A first occurrence appends a new entry
// (Count=1); repeat occurrences of the same key bump Count and LastSeen on
// the existing entry instead of adding a duplicate line — a flapping
// service should read as "halted 14 times", not 14 near-identical entries.
//
// Multiple alerts (or the cron's own callers, once 2.7 lands) can write to
// the same day's key at once, so this retries on a lost
// optimistic-concurrency race rather than clobbering another writer's
// update.
func (q *Queue) UpsertDigestEntry(ctx context.Context, digestDate, key string, entry DigestEntry) error {
	docKey := "revi.digest." + digestDate
	entry.Key = key

	for attempt := 0; attempt < maxDigestWriteAttempts; attempt++ {
		var entries []DigestEntry
		var revision uint64

		existing, err := q.Digest.Get(ctx, docKey)
		switch {
		case errors.Is(err, jetstream.ErrKeyNotFound):
			// first entry of the day
		case err != nil:
			return fmt.Errorf("get digest %s: %w", docKey, err)
		default:
			if err := json.Unmarshal(existing.Value(), &entries); err != nil {
				return fmt.Errorf("decode digest %s: %w", docKey, err)
			}
			revision = existing.Revision()
		}

		if i := indexByKey(entries, key); i >= 0 {
			entries[i].Count++
			entries[i].LastSeen = entry.LastSeen
		} else {
			entry.Count = 1
			entry.FirstSeen = entry.LastSeen
			entries = append(entries, entry)
		}

		data, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("encode digest %s: %w", docKey, err)
		}

		if revision == 0 {
			_, err = q.Digest.Create(ctx, docKey, data)
		} else {
			_, err = q.Digest.Update(ctx, docKey, data, revision)
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("write digest %s: %w", docKey, err)
		}
		// lost a race with a concurrent writer — refetch and retry
	}
	return fmt.Errorf("upsert digest %s: too many concurrent write conflicts", docKey)
}

func indexByKey(entries []DigestEntry, key string) int {
	for i, e := range entries {
		if e.Key == key {
			return i
		}
	}
	return -1
}
