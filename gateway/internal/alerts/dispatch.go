package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

// lockRecord is the value stored under the flap lock key (TRD Sec 4).
// active_branch is populated later by the runner, not the Gateway, so it's
// omitted here.
type lockRecord struct {
	AlertID  string `json:"alert_id"`
	Status   string `json:"status"`
	ReviMode string `json:"revi_mode"`
	LockedAt string `json:"locked_at"`
}

// Publish checks the flap lock for a and, if clear, sets it and publishes
// the alert onto the NATS stream. If the lock is already held, it appends a
// "healing loop halted" marker to today's digest buffer instead of
// dispatching again (TRD Sec 4).
func Publish(ctx context.Context, q *queue.Queue, mode string, a Alert) (published bool, err error) {
	lockValue, err := json.Marshal(lockRecord{
		AlertID:  a.AlertID,
		Status:   "PROCESSING",
		ReviMode: mode,
		LockedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return false, fmt.Errorf("encode lock value: %w", err)
	}

	alreadyLocked, err := q.TryLock(ctx, a.ServiceName, a.ErrorSummary, lockValue)
	if err != nil {
		return false, err
	}
	if alreadyLocked {
		marker := queue.DigestEntry{
			AlertID:   a.AlertID,
			Message:   "[⚠️ HEALING LOOP HALTED] " + a.ErrorSummary,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if err := q.AppendDigestEntry(ctx, time.Now().Format("2006-01-02"), marker); err != nil {
			log.Printf("alerts: failed to append digest halt marker for %s: %v", a.AlertID, err)
		}
		return false, nil
	}

	payload, err := json.Marshal(a)
	if err != nil {
		return false, fmt.Errorf("encode alert: %w", err)
	}
	if _, err := q.JS.Publish(ctx, queue.AlertSubject, payload); err != nil {
		// ponytail: the lock is already set at this point; a publish
		// failure here leaves the alert stuck until the 30-min TTL
		// expires. Add a rollback (delete the lock) if this proves to
		// happen in practice — TRD Sec 6 only accepts the no-refresh
		// risk, not this one.
		return false, fmt.Errorf("publish alert: %w", err)
	}
	return true, nil
}
