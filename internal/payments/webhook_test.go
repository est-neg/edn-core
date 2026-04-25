package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
)

type fakePaymentRepo struct {
	upserts int
}

func (r *fakePaymentRepo) Upsert(_ context.Context, _ checkout.Payment) error {
	r.upserts++
	return nil
}

func (r *fakePaymentRepo) GetByTransactionNSU(_ context.Context, _ string) (*checkout.Payment, error) {
	return nil, errors.New("not implemented")
}

type fakeSubscriptionRepo struct{}

func (fakeSubscriptionRepo) Create(context.Context, checkout.Subscription) error { return nil }
func (fakeSubscriptionRepo) GetByOriginOrderNSU(context.Context, string) (*checkout.Subscription, error) {
	return nil, errors.New("not implemented")
}
func (fakeSubscriptionRepo) Activate(context.Context, string, time.Time, time.Time, time.Time) error {
	return nil
}

type fakeWebhookEventRepo struct {
	events      []checkout.WebhookEvent
	insertCalls int
	markCalls   int
}

func (r *fakeWebhookEventRepo) Insert(_ context.Context, event checkout.WebhookEvent) error {
	r.insertCalls++
	r.events = append(r.events, event)
	return nil
}

func (r *fakeWebhookEventRepo) ExistsByEventHash(_ context.Context, eventHash string) (bool, error) {
	for _, event := range r.events {
		if event.EventHash == eventHash {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeWebhookEventRepo) FindByTransactionNSU(_ context.Context, transactionNSU string) ([]checkout.WebhookEvent, error) {
	var matched []checkout.WebhookEvent
	for _, event := range r.events {
		if event.TransactionNSU == transactionNSU {
			matched = append(matched, event)
		}
	}
	return matched, nil
}

func (r *fakeWebhookEventRepo) MarkProcessed(_ context.Context, id string, processedAt time.Time) error {
	r.markCalls++
	for i := range r.events {
		if r.events[i].EventID == id {
			r.events[i].ProcessedAt = &processedAt
			return nil
		}
	}
	return nil
}

type fakeOutboxRepo struct {
	inserts int
}

func (r *fakeOutboxRepo) Insert(_ context.Context, _ checkout.OutboxEvent) error {
	r.inserts++
	return nil
}

func (r *fakeOutboxRepo) FetchUnpublished(context.Context, int) ([]checkout.OutboxEvent, error) {
	return nil, nil
}

func (r *fakeOutboxRepo) MarkPublished(context.Context, string, time.Time) error { return nil }

type fakeWebhookIdempotencyStore struct{}

func (fakeWebhookIdempotencyStore) CheckWebhookDedup(context.Context, string, string) (bool, bool, error) {
	return false, false, nil
}
func (fakeWebhookIdempotencyStore) SetWebhookTxSeen(context.Context, string) error   { return nil }
func (fakeWebhookIdempotencyStore) SetWebhookHashSeen(context.Context, string) error { return nil }

type fakeVerifyProvider struct {
	verifyCalls int
	response    InfinitePayVerifyResponse
}

func (p *fakeVerifyProvider) CreateCheckout(context.Context, InfinitePayCheckoutRequest) (InfinitePayCheckoutResponse, error) {
	return InfinitePayCheckoutResponse{}, errors.New("not implemented")
}

func (p *fakeVerifyProvider) VerifyPayment(_ context.Context, _, _, _ string) (InfinitePayVerifyResponse, error) {
	p.verifyCalls++
	return p.response, nil
}

func TestWebhookService_Handle_RecoversFromUnprocessedTransactionEvent(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:            primitive.NewObjectID(),
		OrderNSU:      "order-123",
		PlanID:        "plan-basic",
		PlanSlug:      "basic",
		BillingCycle:  "monthly",
		AmountCents:   9900,
		CustomerEmail: "joao@example.com",
		Status:        string(OrderStatusCheckoutCreated),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	webhooks := &fakeWebhookEventRepo{events: []checkout.WebhookEvent{{
		EventID:        "evt-pending",
		Provider:       "infinitepay",
		EventHash:      "old-hash",
		OrderNSU:       "order-123",
		TransactionNSU: "tx-123",
		ReceivedAt:     time.Now().UTC().Add(-time.Minute),
	}}}
	outbox := &fakeOutboxRepo{}
	paymentsRepo := &fakePaymentRepo{}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{Status: PaymentStatusApproved, PaidAmountCents: 9900, ReceiptURL: "https://receipt.example/123"}}
	service := NewWebhookService(
		orders,
		paymentsRepo,
		fakeSubscriptionRepo{},
		webhooks,
		outbox,
		fakeWebhookIdempotencyStore{},
		fakeLockManager{},
		fakeStatusCache{},
		provider,
		zap.NewNop(),
	)

	raw := []byte(`{"transaction_id":"tx-123","order_id":"order-123","invoice_id":"inv-123"}`)
	if err := service.Handle(context.Background(), raw); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if provider.verifyCalls != 1 {
		t.Fatalf("expected verify to be called once, got %d", provider.verifyCalls)
	}
	if paymentsRepo.upserts != 1 {
		t.Fatalf("expected one payment upsert, got %d", paymentsRepo.upserts)
	}
	if outbox.inserts != 1 {
		t.Fatalf("expected one outbox insert, got %d", outbox.inserts)
	}
	if webhooks.insertCalls != 0 {
		t.Fatalf("expected no new webhook event insert, got %d", webhooks.insertCalls)
	}
	if webhooks.markCalls != 1 {
		t.Fatalf("expected one processed mark, got %d", webhooks.markCalls)
	}
}

func TestWebhookService_Handle_IgnoresProcessedTransactionDuplicate(t *testing.T) {
	processedAt := time.Now().UTC().Add(-time.Minute)
	webhooks := &fakeWebhookEventRepo{events: []checkout.WebhookEvent{{
		EventID:        "evt-processed",
		Provider:       "infinitepay",
		EventHash:      "hash-processed",
		OrderNSU:       "order-123",
		TransactionNSU: "tx-123",
		ReceivedAt:     time.Now().UTC().Add(-2 * time.Minute),
		ProcessedAt:    &processedAt,
	}}}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{Status: PaymentStatusApproved, PaidAmountCents: 9900}}
	service := NewWebhookService(
		newFakeOrderRepo(),
		&fakePaymentRepo{},
		fakeSubscriptionRepo{},
		webhooks,
		&fakeOutboxRepo{},
		fakeWebhookIdempotencyStore{},
		fakeLockManager{},
		fakeStatusCache{},
		provider,
		zap.NewNop(),
	)

	raw := []byte(`{"transaction_id":"tx-123","order_id":"order-123","invoice_id":"inv-123"}`)
	if err := service.Handle(context.Background(), raw); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}
	if provider.verifyCalls != 0 {
		t.Fatalf("expected no provider verification on duplicate, got %d", provider.verifyCalls)
	}
}
