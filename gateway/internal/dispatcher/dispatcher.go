// Package dispatcher runs the NATS consumer goroutine that dequeues
// published alerts and fires a GitHub repository_dispatch call for each one
// (TRD Sec 2 step 2).
package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bookang869/Re-vi/gateway/internal/alerts"
	"github.com/bookang869/Re-vi/gateway/internal/github"
	"github.com/bookang869/Re-vi/gateway/internal/metrics"
	"github.com/bookang869/Re-vi/gateway/internal/queue"
	"github.com/bookang869/Re-vi/gateway/internal/store"
)

// eventType must match hermes-triage.yml's `on: repository_dispatch: types:`
// in the target repo.
const eventType = "revi-triage"

// maxDispatchAttempts bounds retries for a persistently-failing dispatch
// (bad token, deleted repo, ...) so it can't loop forever. dispatchRetryDelay
// is a var, not a const, so tests can shrink it — production traffic never
// touches it directly.
//
// ponytail: fixed delay, not exponential backoff. The bug this fixes was
// zero delay (a tight retry storm), not a suboptimal curve; add backoff if
// GitHub API load from retries ever becomes a real concern.
const maxDispatchAttempts = 10

var dispatchRetryDelay = 30 * time.Second

// clientPayload is what the runner reads via github.event.client_payload.*.
// mode is the Gateway's own REVI_MODE (already normalized to PR_REVIEW or
// AUTONOMOUS by config.Load), stamped here rather than looked up as a
// GitHub secret (TRD FR-01).
//
// Ref/MergeTarget are benchmark-only dispatch routing (docs/observability-
// part-b.md), empty on every real alert -- see alerts.Alert's doc comment
// for why they exist and why empty is a safe no-op end to end.
type clientPayload struct {
	Mode         string `json:"mode"`
	AlertID      string `json:"alert_id"`
	ServiceName  string `json:"service_name"`
	TraceID      string `json:"trace_id"`
	ErrorSummary string `json:"error_summary"`
	LogContext   string `json:"log_context"`
	Ref          string `json:"ref,omitempty"`
	MergeTarget  string `json:"merge_target,omitempty"`
}

// Run starts the durable consumer. It blocks only long enough to attach the
// consumer; message handling happens in the background via the returned
// jetstream.ConsumeContext, which the caller should Stop() on shutdown.
// runStore may be nil (observability writes are best-effort, never block
// alert processing — docs/observability-part-a.md).
func Run(ctx context.Context, q *queue.Queue, gh *github.Client, mode string, runStore *store.Store) (jetstream.ConsumeContext, error) {
	return q.Consume(ctx, func(msg jetstream.Msg) {
		var a alerts.Alert
		if err := json.Unmarshal(msg.Data(), &a); err != nil {
			log.Printf("dispatcher: malformed message, dropping: %v", err)
			msg.Term()
			return
		}

		// Repair-enabled toggle (/v1/admin/repair-enabled, queue.RepairEnabled):
		// checked before the dispatch-idempotency marker below, since a
		// toggle-skipped alert was never dispatched -- claiming the
		// "dispatched" marker for it would misrepresent what happened. A
		// read error means we genuinely don't know whether to spend
		// Claude tokens or not, so this retries rather than guessing
		// either way, same posture as the mark-dispatched check below.
		enabled, err := q.RepairEnabled(ctx)
		if err != nil {
			log.Printf("dispatcher: repair-enabled check failed for %s, will retry: %v", a.AlertID, err)
			msg.NakWithDelay(dispatchRetryDelay)
			return
		}
		if !enabled {
			skipDisabled(ctx, q, runStore, a, mode)
			msg.Ack()
			return
		}

		// Idempotency guard (2026-08-19, found during Part A rehearsal
		// testing): a redelivery of this same message -- Gateway restart or
		// slow ack between a successful Dispatch and Ack, or a second alert
		// for this alert_id let through by an expired flap lock -- must not
		// call repository_dispatch a second time. The marker is claimed
		// before dispatching and only rolled back if Dispatch itself fails,
		// so it only ever survives a genuinely successful dispatch.
		markerValue := []byte(fmt.Sprintf(`{"alert_id":%q,"dispatched_at":%q}`, a.AlertID, time.Now().UTC().Format(time.RFC3339)))
		alreadyDispatched, err := q.TryMarkDispatched(ctx, a.AlertID, markerValue)
		if err != nil {
			log.Printf("dispatcher: mark-dispatched check failed for %s, will retry: %v", a.AlertID, err)
			msg.NakWithDelay(dispatchRetryDelay)
			return
		}
		if alreadyDispatched {
			log.Printf("dispatcher: skipping repository_dispatch for %s -- already dispatched", a.AlertID)
			metrics.IncDuplicateDispatchesSkipped()
			msg.Ack()
			return
		}

		payload := clientPayload{
			Mode:         mode,
			AlertID:      a.AlertID,
			ServiceName:  a.ServiceName,
			TraceID:      a.TraceID,
			ErrorSummary: a.ErrorSummary,
			LogContext:   a.LogContext,
			Ref:          a.Ref,
			MergeTarget:  a.MergeTarget,
		}
		if err := gh.Dispatch(ctx, eventType, payload); err != nil {
			if unmarkErr := q.UnmarkDispatched(ctx, a.AlertID); unmarkErr != nil {
				// Bounded by dispatchedTTL (24h), not silent: worst case
				// this alert_id can't be retried until the marker expires.
				log.Printf("dispatcher: failed to roll back dispatch marker for %s after a failed dispatch -- retries blocked until the marker expires: %v", a.AlertID, unmarkErr)
			}
			if md, mderr := msg.Metadata(); mderr == nil && md.NumDelivered >= maxDispatchAttempts {
				log.Printf("dispatcher: giving up on %s after %d attempts: %v", a.AlertID, md.NumDelivered, err)
				msg.Term()
				return
			}
			log.Printf("dispatcher: repository_dispatch failed for %s, will retry: %v", a.AlertID, err)
			msg.NakWithDelay(dispatchRetryDelay)
			return
		}
		metrics.IncModeRouting(mode)
		log.Printf("revi.mode.evaluated alert_id=%s mode=%s", a.AlertID, mode)
		log.Printf("dispatcher: repository_dispatch sent for %s (mode=%s)", a.AlertID, mode)
		msg.Ack()
	})
}

// skipDisabled closes out an alert without ever calling repository_dispatch,
// because the repair-enabled toggle is off. It mirrors what digest.NewHandler
// does for a real run's completion -- release the flap lock, record a
// terminal SQLite outcome, write a digest entry -- so a skipped alert reads
// as a deliberate, closed-out event rather than a hung or invisible one
// (2026-08-24 grilling session). All three side effects are best-effort,
// same convention as the rest of this codebase: logged on failure, never
// blocking the caller.
func skipDisabled(ctx context.Context, q *queue.Queue, runStore *store.Store, a alerts.Alert, mode string) {
	lockKey := queue.LockKey(a.ServiceName, a.ErrorSummary)
	if err := q.Locks.Delete(ctx, lockKey); err != nil {
		log.Printf("dispatcher: failed to release flap lock for %s after repair-disabled skip: %v", a.AlertID, err)
	}

	runStore.RecordOutcome(ctx, store.RunOutcome{
		AlertID: a.AlertID,
		Outcome: "SKIPPED_OFF",
		Summary: "Hermes dispatch skipped: repair-enabled toggle is off",
	})

	digestEntry := queue.DigestEntry{
		AlertID:  a.AlertID,
		Message:  fmt.Sprintf("[%s/SKIPPED_OFF] %s: repair disabled", mode, a.ErrorSummary),
		LastSeen: time.Now().UTC().Format(time.RFC3339),
	}
	if err := q.UpsertDigestEntry(ctx, time.Now().Format("2006-01-02"), lockKey, digestEntry); err != nil {
		log.Printf("dispatcher: failed to write digest entry for %s after repair-disabled skip: %v", a.AlertID, err)
	}

	metrics.IncRepairDisabledSkip()
	log.Printf("revi.repair.disabled alert_id=%s mode=%s", a.AlertID, mode)
}
