package payments

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub"
	"go.uber.org/zap"
)

type pubsubPublisher struct {
	topic *pubsub.Topic
	log   *zap.Logger
}

// NewPubSubPublisher returns a PaymentEventPublisher backed by Google Pub/Sub.
func NewPubSubPublisher(client *pubsub.Client, topicID string, log *zap.Logger) (PaymentEventPublisher, error) {
	topic := client.Topic(topicID)
	return &pubsubPublisher{topic: topic, log: log}, nil
}

func (p *pubsubPublisher) PublishPaymentApproved(ctx context.Context, event PaymentApprovedEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal payment approved event: %w", err)
	}
	msg := &pubsub.Message{
		Data: body,
		Attributes: map[string]string{
			"event_type": "payment.approved",
			"event_id":   event.EventID,
		},
	}
	result := p.topic.Publish(ctx, msg)
	if _, err := result.Get(ctx); err != nil {
		return fmt.Errorf("publish payment approved: %w", err)
	}
	return nil
}

// noopPublisher discards all events. Used when Pub/Sub is not configured (local dev).
type noopPublisher struct {
	log *zap.Logger
}

// NewNoopPublisher returns a PaymentEventPublisher that logs and discards events.
func NewNoopPublisher(log *zap.Logger) PaymentEventPublisher {
	return &noopPublisher{log: log}
}

func (n *noopPublisher) PublishPaymentApproved(_ context.Context, event PaymentApprovedEvent) error {
	n.log.Warn("pubsub disabled — event discarded",
		zap.String("event_type", "payment.approved"),
		zap.String("order_nsu", event.OrderNSU),
	)
	return nil
}
