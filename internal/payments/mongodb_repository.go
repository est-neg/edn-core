package payments

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/idempotency"
	"github.com/villenneve/vil-core/internal/platform/config"
)

// orderRepo adapts checkout.OrderRepository to payments.OrderRepository.
type orderRepo struct{ inner checkout.OrderRepository }

func NewMongoOrderRepository(client *mongo.Client, cfg config.MongoConfig) OrderRepository {
	return &orderRepo{inner: checkout.NewMongoOrderRepository(client, cfg)}
}

func (r *orderRepo) Create(ctx context.Context, order checkout.Order) error {
	return r.inner.Create(ctx, order)
}

func (r *orderRepo) GetByNSU(ctx context.Context, orderNSU string) (*checkout.Order, error) {
	order, err := r.inner.FindByNSU(ctx, orderNSU)
	if errors.Is(err, checkout.ErrOrderNotFound) {
		return nil, ErrOrderNotFound
	}
	return order, err
}

func (r *orderRepo) FindByIntentKey(ctx context.Context, intentKey string) (*checkout.Order, error) {
	order, err := r.inner.FindByIntentKey(ctx, intentKey)
	if errors.Is(err, checkout.ErrOrderNotFound) {
		return nil, ErrOrderNotFound
	}
	return order, err
}

func (r *orderRepo) UpdateStatus(ctx context.Context, orderNSU string, status OrderStatus, updatedAt time.Time) error {
	return r.inner.UpdateStatus(ctx, orderNSU, string(status), updatedAt)
}

func (r *orderRepo) UpdateProviderURL(ctx context.Context, orderNSU, checkoutURL, invoiceSlug string, updatedAt time.Time) error {
	return r.inner.UpdateProviderURL(ctx, orderNSU, checkoutURL, invoiceSlug, updatedAt)
}

func (r *orderRepo) UpdateReceipt(ctx context.Context, orderNSU, receiptURL string, updatedAt time.Time) error {
	return r.inner.UpdateReceipt(ctx, orderNSU, receiptURL, updatedAt)
}

func (r *orderRepo) MarkProviderCreateAttempted(ctx context.Context, orderNSU string, attemptedAt time.Time) error {
	return r.inner.MarkProviderCreateAttempted(ctx, orderNSU, attemptedAt)
}

func (r *orderRepo) FindByCustomerDocument(ctx context.Context, normalizedDocument string) ([]checkout.Order, error) {
	return r.inner.FindByCustomerDocument(ctx, normalizedDocument)
}

func (r *orderRepo) FindOpenByBusinessFingerprint(ctx context.Context, tenantID, normalizedCPF, planSlug, billingCycle string) ([]checkout.Order, error) {
	return r.inner.FindOpenByBusinessFingerprint(ctx, tenantID, normalizedCPF, planSlug, billingCycle)
}

// checkoutIdempotencyRepo adapts idempotency.Repository to payments.CheckoutIdempotencyRepository.
type checkoutIdempotencyRepo struct{ inner idempotency.Repository }

func NewMongoCheckoutIdempotencyRepository(client *mongo.Client, cfg config.MongoConfig) CheckoutIdempotencyRepository {
	return &checkoutIdempotencyRepo{inner: idempotency.NewMongoRepository(client, cfg)}
}

func (r *checkoutIdempotencyRepo) Reserve(ctx context.Context, key idempotency.Key) error {
	return r.inner.Reserve(ctx, key)
}

func (r *checkoutIdempotencyRepo) FindByTenantOpKey(ctx context.Context, tenantID, operation, idempotencyKey string) (*idempotency.Key, error) {
	return r.inner.FindByTenantOpKey(ctx, tenantID, operation, idempotencyKey)
}

func (r *checkoutIdempotencyRepo) FindByTenantOpResourceID(ctx context.Context, tenantID, operation, resourceID string) (*idempotency.Key, error) {
	return r.inner.FindByTenantOpResourceID(ctx, tenantID, operation, resourceID)
}

func (r *checkoutIdempotencyRepo) Commit(ctx context.Context, tenantID, operation, idempotencyKey, resourceID, resourceStatus, resourceURL, externalRef string, updatedAt time.Time) error {
	return r.inner.Commit(ctx, tenantID, operation, idempotencyKey, resourceID, resourceStatus, resourceURL, externalRef, updatedAt)
}

func (r *checkoutIdempotencyRepo) Fail(ctx context.Context, tenantID, operation, idempotencyKey string, updatedAt time.Time) error {
	return r.inner.Fail(ctx, tenantID, operation, idempotencyKey, updatedAt)
}

// paymentRepo adapts checkout.PaymentRepository to payments.PaymentRepository.
type paymentRepo struct{ inner checkout.PaymentRepository }

func NewMongoPaymentRepository(client *mongo.Client, cfg config.MongoConfig) PaymentRepository {
	return &paymentRepo{inner: checkout.NewMongoPaymentRepository(client, cfg)}
}

func (r *paymentRepo) Upsert(ctx context.Context, payment checkout.Payment) error {
	return r.inner.Upsert(ctx, payment)
}

func (r *paymentRepo) GetByTransactionNSU(ctx context.Context, transactionNSU string) (*checkout.Payment, error) {
	return r.inner.FindByTransactionNSU(ctx, transactionNSU)
}

func (r *paymentRepo) GetByOrderNSU(ctx context.Context, orderNSU string) ([]checkout.Payment, error) {
	return r.inner.FindByOrderNSU(ctx, orderNSU)
}

// subscriptionRepo adapts checkout.SubscriptionRepository to payments.SubscriptionRepository.
type subscriptionRepo struct {
	inner checkout.SubscriptionRepository
}

func NewMongoSubscriptionRepository(client *mongo.Client, cfg config.MongoConfig) SubscriptionRepository {
	return &subscriptionRepo{inner: checkout.NewMongoSubscriptionRepository(client, cfg)}
}

func (r *subscriptionRepo) Create(ctx context.Context, sub checkout.Subscription) error {
	return r.inner.Create(ctx, sub)
}

func (r *subscriptionRepo) GetByOriginOrderNSU(ctx context.Context, orderNSU string) (*checkout.Subscription, error) {
	return r.inner.FindByOriginOrderNSU(ctx, orderNSU)
}

func (r *subscriptionRepo) Activate(ctx context.Context, originOrderNSU string, startsAt, endsAt, updatedAt time.Time) error {
	return r.inner.Activate(ctx, originOrderNSU, startsAt, endsAt, updatedAt)
}

// webhookRepo adapts checkout.WebhookEventRepository to payments.WebhookEventRepository.
type webhookRepo struct {
	inner checkout.WebhookEventRepository
}

func NewMongoWebhookEventRepository(client *mongo.Client, cfg config.MongoConfig) WebhookEventRepository {
	return &webhookRepo{inner: checkout.NewMongoWebhookEventRepository(client, cfg)}
}

func (r *webhookRepo) Insert(ctx context.Context, event checkout.WebhookEvent) error {
	return r.inner.Insert(ctx, event)
}

func (r *webhookRepo) ExistsByEventHash(ctx context.Context, eventHash string) (bool, error) {
	return r.inner.ExistsByEventHash(ctx, eventHash)
}

func (r *webhookRepo) FindByTransactionNSU(ctx context.Context, transactionNSU string) ([]checkout.WebhookEvent, error) {
	return r.inner.FindByTransactionNSU(ctx, transactionNSU)
}

func (r *webhookRepo) MarkProcessed(ctx context.Context, id string, processedAt time.Time) error {
	return r.inner.MarkProcessed(ctx, id, processedAt)
}

// outboxRepo adapts checkout.OutboxEventRepository to payments.OutboxEventRepository.
type outboxRepo struct {
	inner checkout.OutboxEventRepository
}

func NewMongoOutboxEventRepository(client *mongo.Client, cfg config.MongoConfig) OutboxEventRepository {
	return &outboxRepo{inner: checkout.NewMongoOutboxEventRepository(client, cfg)}
}

func (r *outboxRepo) Insert(ctx context.Context, event checkout.OutboxEvent) error {
	return r.inner.Insert(ctx, event)
}

func (r *outboxRepo) FetchUnpublished(ctx context.Context, limit int) ([]checkout.OutboxEvent, error) {
	return r.inner.FetchUnpublished(ctx, limit)
}

func (r *outboxRepo) MarkPublished(ctx context.Context, id string, publishedAt time.Time) error {
	return r.inner.MarkPublished(ctx, id, publishedAt)
}

// checkoutStoreAdapter wraps checkout.CheckoutStore to implement payments port interfaces.
type checkoutStoreAdapter struct{ store checkout.CheckoutStore }

// NewCheckoutStoreAdapter returns an adapter that implements IdempotencyStore, LockManager, and StatusCache.
func NewCheckoutStoreAdapter(store checkout.CheckoutStore) interface {
	IdempotencyStore
	LockManager
	StatusCache
} {
	return &checkoutStoreAdapter{store: store}
}

func (a *checkoutStoreAdapter) CheckWebhookDedup(ctx context.Context, transactionNSU, eventHash string) (bool, bool, error) {
	return a.store.CheckWebhookDedup(ctx, transactionNSU, eventHash)
}

func (a *checkoutStoreAdapter) SetWebhookTxSeen(ctx context.Context, transactionNSU string) error {
	return a.store.SetWebhookTxSeen(ctx, transactionNSU)
}

func (a *checkoutStoreAdapter) SetWebhookHashSeen(ctx context.Context, eventHash string) error {
	return a.store.SetWebhookHashSeen(ctx, eventHash)
}

func (a *checkoutStoreAdapter) AcquireOrderLock(ctx context.Context, orderNSU, token string) error {
	return a.store.AcquireOrderLock(ctx, orderNSU, token)
}

func (a *checkoutStoreAdapter) ReleaseOrderLock(ctx context.Context, orderNSU, token string) error {
	return a.store.ReleaseOrderLock(ctx, orderNSU, token)
}

func (a *checkoutStoreAdapter) AcquireFingerprintLock(ctx context.Context, fingerprintHash, token string) error {
	return a.store.AcquireFingerprintLock(ctx, fingerprintHash, token)
}

func (a *checkoutStoreAdapter) ReleaseFingerprintLock(ctx context.Context, fingerprintHash, token string) error {
	return a.store.ReleaseFingerprintLock(ctx, fingerprintHash, token)
}

func (a *checkoutStoreAdapter) GetOrderStatus(ctx context.Context, orderNSU string) (string, bool, error) {
	return a.store.GetOrderStatus(ctx, orderNSU)
}

func (a *checkoutStoreAdapter) SetOrderStatus(ctx context.Context, orderNSU, status string) error {
	return a.store.SetOrderStatus(ctx, orderNSU, status)
}

func (a *checkoutStoreAdapter) InvalidateOrderStatus(ctx context.Context, orderNSU string) error {
	return a.store.InvalidateOrderStatus(ctx, orderNSU)
}
