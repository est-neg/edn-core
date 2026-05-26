package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/zap"

	"github.com/google/uuid"
	"github.com/villenneve/vil-core/internal/checkout"
)

// WebhookService processes raw InfinitePay webhook payloads.
// It is responsible for dedup, locking, payment persistence, and outbox publication.
// It does NOT activate subscriptions directly — that is the worker's responsibility.
type WebhookService struct {
	orders        OrderRepository
	payments      PaymentRepository
	subscriptions SubscriptionRepository
	webhooks      WebhookEventRepository
	outbox        OutboxEventRepository
	idem          IdempotencyStore
	lock          LockManager
	cache         StatusCache
	provider      InfinitePayClient
	log           *zap.Logger
}

func NewWebhookService(
	orders OrderRepository,
	payments PaymentRepository,
	subscriptions SubscriptionRepository,
	webhooks WebhookEventRepository,
	outbox OutboxEventRepository,
	idem IdempotencyStore,
	lock LockManager,
	cache StatusCache,
	provider InfinitePayClient,
	log *zap.Logger,
) *WebhookService {
	return &WebhookService{
		orders:        orders,
		payments:      payments,
		subscriptions: subscriptions,
		webhooks:      webhooks,
		outbox:        outbox,
		idem:          idem,
		lock:          lock,
		cache:         cache,
		provider:      provider,
		log:           log,
	}
}

// minimalWebhookPayload is the minimum structure needed to extract identifiers
// from an InfinitePay webhook. Full raw body is persisted as bson.Raw.
type minimalWebhookPayload struct {
	TransactionNSU string `json:"transaction_id"`
	OrderNSU       string `json:"order_id"`
	InvoiceSlug    string `json:"invoice_id"`
}

// Handle processes a raw inbound webhook body.
// Returns nil on success or on known-duplicate (callers should always respond 200/202).
func (s *WebhookService) Handle(ctx context.Context, rawBody []byte) error {
	eventHash := CalculateEventHash(rawBody)

	// Step 1: Parse minimum identifiers — do not trust status from webhook payload
	var minimal minimalWebhookPayload
	if err := json.Unmarshal(rawBody, &minimal); err != nil {
		return fmt.Errorf("%w: malformed webhook payload", ErrInvalidRequest)
	}
	if minimal.TransactionNSU == "" || minimal.OrderNSU == "" {
		return fmt.Errorf("%w: missing transaction_id or order_id", ErrInvalidRequest)
	}
	var webhookEvent checkout.WebhookEvent
	var hasExistingEvent bool

	// Step 2: Redis fast-path dedup
	txSeen, hashSeen, err := s.idem.CheckWebhookDedup(ctx, minimal.TransactionNSU, eventHash)
	if err != nil {
		s.log.Warn("redis dedup check failed, falling through to mongo", zap.Error(err))
	}
	if txSeen || hashSeen {
		txEvents, mongoErr := s.webhooks.FindByTransactionNSU(ctx, minimal.TransactionNSU)
		if mongoErr == nil {
			if processed, existing := selectWebhookEventForProcessing(txEvents); processed {
				s.log.Info("duplicate webhook ignored", zap.String("transaction_nsu", minimal.TransactionNSU))
				return nil
			} else if existing != nil {
				webhookEvent = *existing
				hasExistingEvent = true
			}
		}
	}

	// Step 3: Acquire distributed order lock
	lockToken := uuid.New().String()
	if err := s.lock.AcquireOrderLock(ctx, minimal.OrderNSU, lockToken); err != nil {
		if errors.Is(err, checkout.ErrLockNotAcquired) {
			return fmt.Errorf("%w: order %s", ErrLockConflict, minimal.OrderNSU)
		}
		return fmt.Errorf("acquire lock for order %s: %w", minimal.OrderNSU, err)
	}
	defer s.lock.ReleaseOrderLock(ctx, minimal.OrderNSU, lockToken) //nolint:errcheck

	// Step 4: Authoritative transaction dedup (after lock)
	txEvents, err := s.webhooks.FindByTransactionNSU(ctx, minimal.TransactionNSU)
	if err != nil {
		s.log.Warn("mongo transaction dedup check failed", zap.String("transaction_nsu", minimal.TransactionNSU), zap.Error(err))
	} else {
		processed, existing := selectWebhookEventForProcessing(txEvents)
		if processed {
			s.log.Info("duplicate webhook confirmed in mongo", zap.String("transaction_nsu", minimal.TransactionNSU))
			return nil
		}
		if existing != nil {
			webhookEvent = *existing
			hasExistingEvent = true
		}
	}

	// Step 5: Load order
	order, err := s.orders.GetByNSU(ctx, minimal.OrderNSU)
	if err != nil {
		return fmt.Errorf("order not found for webhook: %w", err)
	}

	// Step 6: Authoritative payment verification via provider
	verified, err := s.provider.VerifyPayment(ctx, minimal.InvoiceSlug, minimal.OrderNSU, minimal.TransactionNSU)
	if err != nil {
		return fmt.Errorf("verify payment: %w", err)
	}

	// Step 6a: Canonical transaction identity check (webhook hint vs. provider-verified).
	// verified.TransactionNSU is the canonical ID returned by payment_check.
	// minimal.TransactionNSU is only a lookup hint from the webhook payload — never canonical truth.
	if minimal.TransactionNSU != "" && verified.TransactionNSU != "" &&
		minimal.TransactionNSU != verified.TransactionNSU {
		return fmt.Errorf("%w: webhook transaction_id does not match provider-verified canonical transaction_id",
			ErrTransactionMismatch)
	}

	// Step 6b: Persisted canonical transaction identity check.
	// If core already has a payment for this order with a different transaction_nsu, reject.
	if verified.TransactionNSU != "" {
		existingPayments, lookupErr := s.payments.GetByOrderNSU(ctx, minimal.OrderNSU)
		if lookupErr != nil {
			return fmt.Errorf("check canonical transaction identity: %w", lookupErr)
		}
		for _, p := range existingPayments {
			if p.TransactionNSU != verified.TransactionNSU {
				return fmt.Errorf("%w: persisted canonical transaction_nsu does not match provider-verified canonical transaction_id",
					ErrTransactionMismatch)
			}
		}
		// If no existing payment, the verified transaction ID becomes canonical on upsert below.
	}

	// Step 7: Amount validation — hard integrity gate; no mutation is permitted on mismatch.
	if verified.PaidAmountCents != order.AmountCents {
		return fmt.Errorf("%w: order expects %d cents, provider verified %d cents",
			ErrAmountMismatch, order.AmountCents, verified.PaidAmountCents)
	}

	now := time.Now().UTC()
	rawBSON, _ := bson.Marshal(bson.M{"raw": string(rawBody)})

	// Step 8: Persist raw webhook event (renumbered after hardening steps 6a/6b/7 above)
	if !hasExistingEvent {
		webhookEvent = checkout.WebhookEvent{
			EventID:        uuid.New().String(),
			Provider:       "infinitepay",
			EventHash:      eventHash,
			OrderNSU:       minimal.OrderNSU,
			TransactionNSU: minimal.TransactionNSU,
			Status:         string(verified.Status),
			RawPayload:     rawBSON,
			ReceivedAt:     now,
		}
		if err := s.webhooks.Insert(ctx, webhookEvent); err != nil {
			// ErrDuplicateWebhook from Mongo means concurrent duplicate — safe to ignore
			if err == checkout.ErrDuplicateWebhook {
				return nil
			}
			return fmt.Errorf("persist webhook event: %w", err)
		}
	}

	// Step 9: Set Redis dedup keys
	s.idem.SetWebhookTxSeen(ctx, minimal.TransactionNSU) //nolint:errcheck
	s.idem.SetWebhookHashSeen(ctx, eventHash)            //nolint:errcheck

	// Amount match is guaranteed by the hard gate in Step 7 above.
	paymentStatus := verified.Status
	orderStatus := mapPaymentToOrderStatus(verified.Status)

	// Step 10: Upsert payment — canonical transaction_nsu is strictly the provider-verified value.
	// The adapter guarantees non-empty; the webhook hint is never a fallback for canonical identity.
	canonicalTransactionNSU := verified.TransactionNSU
	payment := checkout.Payment{
		PaymentID:       uuid.New().String(),
		OrderNSU:        minimal.OrderNSU,
		TransactionNSU:  canonicalTransactionNSU,
		InvoiceSlug:     minimal.InvoiceSlug,
		AmountCents:     order.AmountCents,
		PaidAmountCents: verified.PaidAmountCents,
		Status:          string(paymentStatus),
		ReceiptURL:      verified.ReceiptURL,
		RawPayload:      rawBSON,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.payments.Upsert(ctx, payment); err != nil {
		return fmt.Errorf("upsert payment: %w", err)
	}

	// Step 11: Update order status — failure is not safe to ignore; returning success to the
	// provider when durable state has not been applied would create a false-success signal.
	if err := s.orders.UpdateStatus(ctx, minimal.OrderNSU, orderStatus, now); err != nil {
		return fmt.Errorf("update order status: %w", err)
	}

	// Step 12: If approved, enqueue outbox event for worker.
	// Amount match is enforced as a hard gate above — this path is reached only on exact match.
	// The outbox EventID is derived deterministically from webhookEvent.EventID so that retries
	// produce the same ID and ErrDuplicateOutbox can be treated as a successful no-op.
	if verified.Status == PaymentStatusApproved {
		event := PaymentApprovedEvent{
			EventID:         uuid.NewSHA1(uuid.Nil, []byte("payment.approved:"+webhookEvent.EventID)).String(),
			OrderNSU:        minimal.OrderNSU,
			TransactionNSU:  minimal.TransactionNSU,
			PlanSlug:        order.PlanSlug,
			PlanID:          order.PlanID,
			AmountCents:     order.AmountCents,
			PaidAmountCents: verified.PaidAmountCents,
			CustomerEmail:   order.CustomerEmail,
			OccurredAt:      now,
		}
		eventPayload, _ := json.Marshal(event)
		outboxEvt := checkout.OutboxEvent{
			EventID:   uuid.NewSHA1(uuid.Nil, []byte("outbox.payment.approved:"+webhookEvent.EventID)).String(),
			EventType: "payment.approved",
			Payload:   eventPayload,
			Published: false,
			CreatedAt: now,
		}
		if err := s.outbox.Insert(ctx, outboxEvt); err != nil {
			if err == checkout.ErrDuplicateOutbox {
				// Idempotent retry: event already enqueued on a previous attempt — treat as success.
				s.log.Info("outbox event already enqueued (duplicate), skipping", zap.String("order_nsu", minimal.OrderNSU))
			} else {
				return fmt.Errorf("enqueue outbox event: %w", err)
			}
		}
	}

	// Step 13: Invalidate Redis status cache
	s.cache.InvalidateOrderStatus(ctx, minimal.OrderNSU) //nolint:errcheck

	// Step 14: Mark webhook processed
	s.webhooks.MarkProcessed(ctx, webhookEvent.EventID, now) //nolint:errcheck

	s.log.Info("webhook processed",
		zap.String("order_nsu", minimal.OrderNSU),
		zap.String("transaction_nsu", minimal.TransactionNSU),
		zap.String("payment_status", string(paymentStatus)),
		zap.String("order_status", string(orderStatus)),
	)
	return nil
}

func selectWebhookEventForProcessing(events []checkout.WebhookEvent) (bool, *checkout.WebhookEvent) {
	for i := range events {
		if events[i].ProcessedAt != nil {
			return true, nil
		}
	}
	if len(events) == 0 {
		return false, nil
	}
	selected := events[0]
	for i := 1; i < len(events); i++ {
		if events[i].ReceivedAt.Before(selected.ReceivedAt) {
			selected = events[i]
		}
	}
	return false, &selected
}

func mapPaymentToOrderStatus(ps PaymentStatus) OrderStatus {
	switch ps {
	case PaymentStatusApproved:
		return OrderStatusPaid
	case PaymentStatusRejected:
		return OrderStatusFailed
	case PaymentStatusPendingReview:
		return OrderStatusPendingReview
	default:
		return OrderStatusPending
	}
}
