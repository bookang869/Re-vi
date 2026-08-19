package queue

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/nats-io/nats.go/jetstream"
)

// DispatchedKey builds the dispatch-marker KV key for an alert_id. Hashed
// for the same reason LockKey hashes error_summary: alert_id is
// alertname+fingerprint, and alertname is free-form label text that may
// contain characters NATS KV keys don't allow.
func DispatchedKey(alertID string) string {
	h := fnv.New64a()
	h.Write([]byte(alertID))
	return fmt.Sprintf("revi.dispatched.alert.%s", hex.EncodeToString(h.Sum(nil)))
}

// TryMarkDispatched claims the dispatch marker for alertID. alreadyDispatched
// = true means a previous delivery of this alert already completed a
// successful repository_dispatch call — the caller must not dispatch again,
// only acknowledge and move on.
//
// The marker is only ever Created here, never Updated — see UnmarkDispatched
// for the rollback path when the dispatch attempt this claims for fails.
func (q *Queue) TryMarkDispatched(ctx context.Context, alertID string, value []byte) (alreadyDispatched bool, err error) {
	_, err = q.Dispatched.Create(ctx, DispatchedKey(alertID), value)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, jetstream.ErrKeyExists):
		return true, nil
	default:
		return false, fmt.Errorf("mark dispatched: %w", err)
	}
}

// UnmarkDispatched removes alertID's dispatch marker. Called when the
// repository_dispatch call the marker was claimed for actually failed, so a
// legitimate retry isn't mistaken for an already-completed duplicate.
func (q *Queue) UnmarkDispatched(ctx context.Context, alertID string) error {
	if err := q.Dispatched.Delete(ctx, DispatchedKey(alertID)); err != nil {
		return fmt.Errorf("unmark dispatched: %w", err)
	}
	return nil
}
