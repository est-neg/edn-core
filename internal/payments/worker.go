package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/google/uuid"
	"github.com/villenneve/vil-core/internal/checkout"
)

// ActivationService activates subscriptions in response to payment.approved events.
// It is called by the subscription-worker for each consumed Pub/Sub message.
type ActivationService struct {
	orders        OrderRepository
	subscriptions SubscriptionRepository
	cache         StatusCache
	log           *zap.Logger
}

func NewActivationService(
	orders OrderRepository,
	subscriptions SubscriptionRepository,
	cache StatusCache,
	log *zap.Logger,
) *ActivationService {
	return &ActivationService{
		orders:        orders,
		subscriptions: subscriptions,
		cache:         cache,
		log:           log,
	}
}

// Activate processes a PaymentApprovedEvent.
// It is idempotent: if the subscription is already active for this order, it returns nil.
func (s *ActivationService) Activate(ctx context.Context, event PaymentApprovedEvent) error {
	// Check if subscription already active
	existing, err := s.subscriptions.GetByOriginOrderNSU(ctx, event.OrderNSU)
	if err == nil && existing != nil && existing.Status == string(SubscriptionStatusActive) {
		s.log.Info("subscription already active, skipping", zap.String("order_nsu", event.OrderNSU))
		return nil
	}

	order, err := s.orders.GetByNSU(ctx, event.OrderNSU)
	if err != nil {
		return fmt.Errorf("get order for activation %s: %w", event.OrderNSU, err)
	}

	now := time.Now().UTC()
	startsAt := now
	endsAt := calculateEndsAt(now, order.BillingCycle)

	// Create or ensure subscription exists
	if existing == nil {
		sub := checkout.Subscription{
			SubscriptionID: uuid.New().String(),
			CustomerEmail:  order.CustomerEmail,
			PlanID:         order.PlanID,
			PlanSlug:       order.PlanSlug,
			Status:         string(SubscriptionStatusPendingActivation),
			OriginOrderNSU: event.OrderNSU,
			StartsAt:       startsAt,
			EndsAt:         endsAt,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := s.subscriptions.Create(ctx, sub); err != nil {
			// If duplicate, it was created by a concurrent delivery — safe to continue
			s.log.Warn("subscription create duplicate on activation", zap.String("order_nsu", event.OrderNSU), zap.Error(err))
		}
	}

	// Activate subscription
	if err := s.subscriptions.Activate(ctx, event.OrderNSU, startsAt, endsAt, now); err != nil {
		return fmt.Errorf("activate subscription for order %s: %w", event.OrderNSU, err)
	}

	// Invalidate cache
	s.cache.InvalidateOrderStatus(ctx, event.OrderNSU) //nolint:errcheck

	s.log.Info("subscription activated",
		zap.String("order_nsu", event.OrderNSU),
		zap.String("customer_email", event.CustomerEmail),
		zap.String("plan_slug", event.PlanSlug),
	)
	return nil
}

// OutboxDispatcher polls the outbox and publishes pending events.
type OutboxDispatcher struct {
	outbox    OutboxEventRepository
	publisher PaymentEventPublisher
	log       *zap.Logger
}

func NewOutboxDispatcher(outbox OutboxEventRepository, publisher PaymentEventPublisher, log *zap.Logger) *OutboxDispatcher {
	return &OutboxDispatcher{outbox: outbox, publisher: publisher, log: log}
}

// Dispatch fetches and publishes up to batchSize unpublished outbox events.
func (d *OutboxDispatcher) Dispatch(ctx context.Context, batchSize int) error {
	events, err := d.outbox.FetchUnpublished(ctx, batchSize)
	if err != nil {
		return fmt.Errorf("fetch unpublished outbox events: %w", err)
	}
	for _, evt := range events {
		if evt.EventType != "payment.approved" {
			d.log.Warn("unknown outbox event type, skipping", zap.String("event_type", evt.EventType))
			continue
		}
		var approved PaymentApprovedEvent
		if err := json.Unmarshal(evt.Payload, &approved); err != nil {
			d.log.Error("failed to unmarshal outbox event payload", zap.String("event_id", evt.EventID), zap.Error(err))
			continue
		}
		if err := d.publisher.PublishPaymentApproved(ctx, approved); err != nil {
			d.log.Error("failed to publish payment approved event", zap.String("event_id", evt.EventID), zap.Error(err))
			continue
		}
		now := time.Now().UTC()
		if err := d.outbox.MarkPublished(ctx, evt.EventID, now); err != nil {
			d.log.Error("failed to mark outbox event published", zap.String("event_id", evt.EventID), zap.Error(err))
		}
	}
	return nil
}

func calculateEndsAt(from time.Time, billingCycle string) time.Time {
	switch billingCycle {
	case "annual":
		return from.AddDate(1, 0, 0)
	case "monthly":
		return from.AddDate(0, 1, 0)
	}
	return from.AddDate(0, 1, 0)
}
