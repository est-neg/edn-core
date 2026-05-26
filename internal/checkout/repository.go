package checkout

import (
	"context"
	"time"
)

// OrderRepository defines persistence operations for checkout orders.
// All mutations must be performed under an acquired Redis order lock.
type OrderRepository interface {
	Create(ctx context.Context, order Order) error
	FindByNSU(ctx context.Context, orderNSU string) (*Order, error)
	// FindByIntentKey locates an order by its backend-issued checkout_intent_key.
	// Returns ErrOrderNotFound when no matching order exists.
	FindByIntentKey(ctx context.Context, intentKey string) (*Order, error)
	// FindByCustomerDocument returns orders for a given normalized CPF (11 digits),
	// ordered by created_at descending. Returns empty slice when none found.
	// Must only be called from admin-authenticated paths.
	FindByCustomerDocument(ctx context.Context, normalizedDocument string) ([]Order, error)
	// FindOpenByBusinessFingerprint returns non-terminal orders scoped to
	// tenant+CPF+plan_slug+billing_cycle, ordered by created_at descending.
	// Intended for backend-internal dedup only — never call from public search.
	FindOpenByBusinessFingerprint(ctx context.Context, tenantID, normalizedCPF, planSlug, billingCycle string) ([]Order, error)
	UpdateStatus(ctx context.Context, orderNSU, status string, updatedAt time.Time) error
	UpdateProviderURL(ctx context.Context, orderNSU, checkoutURL, invoiceSlug string, updatedAt time.Time) error
	UpdateReceipt(ctx context.Context, orderNSU, receiptURL string, updatedAt time.Time) error
	// MarkProviderCreateAttempted records the timestamp when a provider checkout-create call was
	// first attempted for this order. Must be persisted BEFORE the provider call so that recovery
	// logic can detect partial attempts and avoid unsafe recreation.
	MarkProviderCreateAttempted(ctx context.Context, orderNSU string, attemptedAt time.Time) error
}

// PaymentRepository defines persistence operations for payment transactions.
// Upsert is the primary write path; the unique key is transaction_nsu.
type PaymentRepository interface {
	Upsert(ctx context.Context, payment Payment) error
	FindByTransactionNSU(ctx context.Context, transactionNSU string) (*Payment, error)
	FindByOrderNSU(ctx context.Context, orderNSU string) ([]Payment, error)
	UpdateStatus(ctx context.Context, transactionNSU, status string, updatedAt time.Time) error
}

// SubscriptionRepository defines persistence operations for customer subscriptions.
type SubscriptionRepository interface {
	Create(ctx context.Context, sub Subscription) error
	FindByOriginOrderNSU(ctx context.Context, orderNSU string) (*Subscription, error)
	FindByEmail(ctx context.Context, email string) ([]Subscription, error)
	// Activate sets the subscription to active and records the billing period.
	// It matches by origin_order_nsu and is the canonical activation write path.
	Activate(ctx context.Context, originOrderNSU string, startsAt, endsAt, updatedAt time.Time) error
	UpdateStatus(ctx context.Context, id, status string, updatedAt time.Time) error
}

// WebhookEventRepository defines persistence operations for raw inbound webhook events.
type WebhookEventRepository interface {
	Insert(ctx context.Context, event WebhookEvent) error
	// ExistsByEventHash is the authoritative (Mongo) dedup check.
	// Redis must be checked first as the fast path before calling this.
	ExistsByEventHash(ctx context.Context, eventHash string) (bool, error)
	FindByTransactionNSU(ctx context.Context, transactionNSU string) ([]WebhookEvent, error)
	MarkProcessed(ctx context.Context, id string, processedAt time.Time) error
}

// OutboxEventRepository defines persistence operations for the at-least-once outbox.
type OutboxEventRepository interface {
	Insert(ctx context.Context, event OutboxEvent) error
	// FetchUnpublished returns up to limit unpublished events ordered by created_at ascending.
	// Used by the outbox worker for reliable publication.
	FetchUnpublished(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, id string, publishedAt time.Time) error
}
