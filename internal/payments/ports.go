package payments

import (
	"context"
	"time"

	"github.com/villenneve/vil-core/internal/checkout"
)

// PlanRepository is the read port for plan catalog.
type PlanRepository interface {
	FindActiveBySlugAndCycle(ctx context.Context, slug, billingCycle string) (*checkout.Plan, error)
	ListActive(ctx context.Context) ([]checkout.Plan, error)
}

// OrderRepository is the write/read port for checkout orders.
type OrderRepository interface {
	Create(ctx context.Context, order checkout.Order) error
	GetByNSU(ctx context.Context, orderNSU string) (*checkout.Order, error)
	UpdateStatus(ctx context.Context, orderNSU string, status OrderStatus, updatedAt time.Time) error
	UpdateProviderURL(ctx context.Context, orderNSU, checkoutURL, invoiceSlug string, updatedAt time.Time) error
	UpdateReceipt(ctx context.Context, orderNSU, receiptURL string, updatedAt time.Time) error
}

// PaymentRepository is the write/read port for payments.
type PaymentRepository interface {
	Upsert(ctx context.Context, payment checkout.Payment) error
	GetByTransactionNSU(ctx context.Context, transactionNSU string) (*checkout.Payment, error)
}

// SubscriptionRepository is the write/read port for subscriptions.
type SubscriptionRepository interface {
	Create(ctx context.Context, sub checkout.Subscription) error
	GetByOriginOrderNSU(ctx context.Context, orderNSU string) (*checkout.Subscription, error)
	Activate(ctx context.Context, originOrderNSU string, startsAt, endsAt, updatedAt time.Time) error
}

// WebhookEventRepository is the write port for raw webhook events.
type WebhookEventRepository interface {
	Insert(ctx context.Context, event checkout.WebhookEvent) error
	ExistsByEventHash(ctx context.Context, eventHash string) (bool, error)
	MarkProcessed(ctx context.Context, id string, processedAt time.Time) error
}

// OutboxEventRepository is the write port for the outbox.
type OutboxEventRepository interface {
	Insert(ctx context.Context, event checkout.OutboxEvent) error
	FetchUnpublished(ctx context.Context, limit int) ([]checkout.OutboxEvent, error)
	MarkPublished(ctx context.Context, id string, publishedAt time.Time) error
}

// InfinitePayClient is the port for the InfinitePay provider adapter.
type InfinitePayClient interface {
	CreateCheckout(ctx context.Context, req InfinitePayCheckoutRequest) (InfinitePayCheckoutResponse, error)
	VerifyPayment(ctx context.Context, invoiceSlug, orderNSU, transactionNSU string) (InfinitePayVerifyResponse, error)
}

// IdempotencyStore is the Redis fast-path dedup for webhooks.
type IdempotencyStore interface {
	CheckWebhookDedup(ctx context.Context, transactionNSU, eventHash string) (txSeen, hashSeen bool, err error)
	SetWebhookTxSeen(ctx context.Context, transactionNSU string) error
	SetWebhookHashSeen(ctx context.Context, eventHash string) error
}

// LockManager acquires distributed order locks.
type LockManager interface {
	AcquireOrderLock(ctx context.Context, orderNSU, token string) error
	ReleaseOrderLock(ctx context.Context, orderNSU, token string) error
}

// StatusCache is the Redis order status read-through cache.
type StatusCache interface {
	GetOrderStatus(ctx context.Context, orderNSU string) (string, bool, error)
	SetOrderStatus(ctx context.Context, orderNSU, status string) error
	InvalidateOrderStatus(ctx context.Context, orderNSU string) error
}

// PaymentEventPublisher publishes payment domain events.
type PaymentEventPublisher interface {
	PublishPaymentApproved(ctx context.Context, event PaymentApprovedEvent) error
}

// PaymentEventConsumer subscribes to payment domain events.
type PaymentEventConsumer interface {
	ConsumePaymentApproved(ctx context.Context, handler func(context.Context, PaymentApprovedEvent) error) error
}
