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
type clientPayload struct {
	Mode         string `json:"mode"`
	AlertID      string `json:"alert_id"`
	ServiceName  string `json:"service_name"`
	TraceID      string `json:"trace_id"`
	ErrorSummary string `json:"error_summary"`
	LogContext   string `json:"log_context"`
}

// Run starts the durable consumer. It blocks only long enough to attach the
// consumer; message handling happens in the background via the returned
// jetstream.ConsumeContext, which the caller should Stop() on shutdown.
func Run(ctx context.Context, q *queue.Queue, gh *github.Client, mode string) (jetstream.ConsumeContext, error) {
	return q.Consume(ctx, func(msg jetstream.Msg) {
		var a alerts.Alert
		if err := json.Unmarshal(msg.Data(), &a); err != nil {
			log.Printf("dispatcher: malformed message, dropping: %v", err)
			msg.Term()
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
