package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// RepairEnabledKey is the Config bucket key gating whether the dispatcher
// is allowed to call repository_dispatch (i.e. spend Claude tokens via
// Hermes). Kept in NATS KV, not SQLite, per the project's own convention
// that pipeline-critical decisions live in KV, not the best-effort SQLite
// store.
const RepairEnabledKey = "repair.enabled"

// RepairEnabled reports whether Hermes dispatch is currently enabled. A
// missing key is treated as enabled: this feature must never silently stop
// an already-running pipeline just because nobody has explicitly toggled
// it yet.
func (q *Queue) RepairEnabled(ctx context.Context) (bool, error) {
	entry, err := q.Config.Get(ctx, RepairEnabledKey)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get %s: %w", RepairEnabledKey, err)
	}
	return string(entry.Value()) == "true", nil
}

// SetRepairEnabled sets whether Hermes dispatch is enabled.
func (q *Queue) SetRepairEnabled(ctx context.Context, enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	if _, err := q.Config.Put(ctx, RepairEnabledKey, []byte(value)); err != nil {
		return fmt.Errorf("put %s: %w", RepairEnabledKey, err)
	}
	return nil
}
