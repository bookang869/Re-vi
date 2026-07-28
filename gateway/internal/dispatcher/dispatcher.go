// Package dispatcher runs the NATS consumer goroutine that dequeues
// published alerts and fires a GitHub repository_dispatch call for each one
// (TRD Sec 2 step 2).
package dispatcher

import (
	"context"
	"encoding/json"
	"log"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bookang869/Re-vi/gateway/internal/alerts"
	"github.com/bookang869/Re-vi/gateway/internal/github"
	"github.com/bookang869/Re-vi/gateway/internal/queue"
)

// eventType must match hermes-triage.yml's `on: repository_dispatch: types:`
// in the target repo.
const eventType = "revi-triage"

// clientPayload is what the runner reads via github.event.client_payload.*.
// mode is the Gateway's own REVI_MODE (already normalized to PR_REVIEW or
// AUTONOMOUS by config.Load), stamped here rather than looked up as a
// GitHub secret (TRD FR-01).
type clientPayload struct {
	Mode         string `json:"mode"`
	AlertID      string `json:"alert_id"`
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

		payload := clientPayload{
			Mode:         mode,
			AlertID:      a.AlertID,
			TraceID:      a.TraceID,
			ErrorSummary: a.ErrorSummary,
			LogContext:   a.LogContext,
		}
		if err := gh.Dispatch(ctx, eventType, payload); err != nil {
			log.Printf("dispatcher: repository_dispatch failed for %s, will retry: %v", a.AlertID, err)
			msg.Nak()
			return
		}
		log.Printf("dispatcher: repository_dispatch sent for %s (mode=%s)", a.AlertID, mode)
		msg.Ack()
	})
}
