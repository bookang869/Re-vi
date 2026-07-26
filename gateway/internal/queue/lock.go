package queue

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/nats-io/nats.go/jetstream"
)

// LockKey builds the flap-lock KV key for a (service, error) pair. NATS KV
// keys can't contain ":", so all schemas use "." as the separator; the
// error summary is hashed since raw error text may contain characters KV
// keys don't allow at all.
func LockKey(serviceName, errorSummary string) string {
	h := fnv.New64a()
	h.Write([]byte(errorSummary))
	return fmt.Sprintf("revi.lock.service.%s.error.%s", serviceName, hex.EncodeToString(h.Sum(nil)))
}

// TryLock attempts to acquire the flap lock for (serviceName, errorSummary).
// alreadyLocked=true means a repair is already in flight and the caller
// must halt, not dispatch again.
//
// The lock is only ever Created, never Updated: its TTL is fixed at
// bucket-creation time (see Connect) and deliberately not refreshed while a
// repair runs — TRD Sec 6 accepts that risk rather than adding refresh
// logic.
func (q *Queue) TryLock(ctx context.Context, serviceName, errorSummary string, value []byte) (alreadyLocked bool, err error) {
	_, err = q.Locks.Create(ctx, LockKey(serviceName, errorSummary), value)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, jetstream.ErrKeyExists):
		return true, nil
	default:
		return false, fmt.Errorf("lock: %w", err)
	}
}
