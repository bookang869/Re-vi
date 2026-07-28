package queue

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// consumerName is durable so a Gateway restart resumes from the last
// unacked message instead of replaying the whole stream or skipping ahead.
const consumerName = "dispatcher"

// Consume starts a durable pull consumer on the alerts.triage stream and
// invokes handler for each message. handler is responsible for
// Ack/Nak/Term-ing the message itself.
func (q *Queue) Consume(ctx context.Context, handler jetstream.MessageHandler) (jetstream.ConsumeContext, error) {
	cons, err := q.JS.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:   consumerName,
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer: %w", err)
	}
	return cons.Consume(handler)
}
