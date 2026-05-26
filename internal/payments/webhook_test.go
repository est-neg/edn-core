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
	upserts          int
	upserted         []checkout.Payment // captures full payment passed to Upsert for content assertions
	existingPayments []checkout.Payment // returned by GetByOrderNSU; nil means no existing payments
}

func (r *fakePaymentRepo) Upsert(_ context.Context, p checkout.Payment) error {
	r.upserts++
	r.upserted = append(r.upserted, p)
	return nil
}

func (r *fakePaymentRepo) GetByTransactionNSU(_ context.Context, _ string) (*checkout.Payment, error) {
	return nil, errors.New("not implemented")
}

func (r *fakePaymentRepo) GetByOrderNSU(_ context.Context, _ string) ([]checkout.Payment, error) {
	return r.existingPayments, nil
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
	inserts   int
	insertErr error // when non-nil, returned by Insert
}

func (r *fakeOutboxRepo) Insert(_ context.Context, _ checkout.OutboxEvent) error {
	r.inserts++
	if r.insertErr != nil {
		return r.insertErr
	}
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

// newWebhookServiceForTest wires a WebhookService with the given order and payment repos
// and a provider that returns the provided verify response.
func newWebhookServiceForTest(
	orders *fakeOrderRepo,
	paymentsRepo *fakePaymentRepo,
	provider *fakeVerifyProvider,
) *WebhookService {
	return NewWebhookService(
		orders,
		paymentsRepo,
		fakeSubscriptionRepo{},
		&fakeWebhookEventRepo{},
		&fakeOutboxRepo{},
		fakeWebhookIdempotencyStore{},
		fakeLockManager{},
		fakeStatusCache{},
		provider,
		zap.NewNop(),
	)
}

// TestWebhookService_Handle_RejectsTransactionMismatch_WebhookHintVsProvider verifies that
// when the webhook payload transaction_id differs from the provider-verified canonical
// transaction ID, Handle returns ErrTransactionMismatch with no payment upsert.
func TestWebhookService_Handle_RejectsTransactionMismatch_WebhookHintVsProvider(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:          primitive.NewObjectID(),
		OrderNSU:    "order-123",
		AmountCents: 9900,
		Status:      string(OrderStatusCheckoutCreated),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	paymentsRepo := &fakePaymentRepo{}
	// Provider returns "tx-provider-canonical" — different from webhook's "tx-webhook-hint"
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status:          PaymentStatusApproved,
		PaidAmountCents: 9900,
		TransactionNSU:  "tx-provider-canonical",
	}}
	svc := newWebhookServiceForTest(orders, paymentsRepo, provider)

	raw := []byte(`{"transaction_id":"tx-webhook-hint","order_id":"order-123","invoice_id":"inv-123"}`)
	err := svc.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected ErrTransactionMismatch, got nil")
	}
	if !errors.Is(err, ErrTransactionMismatch) {
		t.Fatalf("expected ErrTransactionMismatch, got %v", err)
	}
	if paymentsRepo.upserts != 0 {
		t.Fatalf("expected no payment upsert on transaction mismatch, got %d", paymentsRepo.upserts)
	}
}

// TestWebhookService_Handle_RejectsTransactionMismatch_PersistedVsProvider verifies that
// when a canonical transaction_nsu is already persisted for the order and the provider
// returns a different transaction ID, Handle returns ErrTransactionMismatch with no upsert.
func TestWebhookService_Handle_RejectsTransactionMismatch_PersistedVsProvider(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:          primitive.NewObjectID(),
		OrderNSU:    "order-123",
		AmountCents: 9900,
		Status:      string(OrderStatusCheckoutCreated),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	// An existing payment for this order with a DIFFERENT canonical transaction_nsu.
	paymentsRepo := &fakePaymentRepo{
		existingPayments: []checkout.Payment{{TransactionNSU: "tx-previously-canonical"}},
	}
	// Provider now returns a different ID — conflict with persisted state.
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status:          PaymentStatusApproved,
		PaidAmountCents: 9900,
		TransactionNSU:  "tx-provider-different",
	}}
	svc := newWebhookServiceForTest(orders, paymentsRepo, provider)

	// Webhook hint matches provider verified (so step 6a passes), but step 6b must fire.
	raw := []byte(`{"transaction_id":"tx-provider-different","order_id":"order-123","invoice_id":"inv-123"}`)
	err := svc.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected ErrTransactionMismatch, got nil")
	}
	if !errors.Is(err, ErrTransactionMismatch) {
		t.Fatalf("expected ErrTransactionMismatch, got %v", err)
	}
	if paymentsRepo.upserts != 0 {
		t.Fatalf("expected no payment upsert on persisted mismatch, got %d", paymentsRepo.upserts)
	}
}

// TestWebhookService_Handle_RejectsAmountMismatch verifies that when the provider-verified
// paid amount does not match the order amount, Handle returns ErrAmountMismatch with no upsert.
func TestWebhookService_Handle_RejectsAmountMismatch(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:          primitive.NewObjectID(),
		OrderNSU:    "order-123",
		AmountCents: 9900,
		Status:      string(OrderStatusCheckoutCreated),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	paymentsRepo := &fakePaymentRepo{}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status:          PaymentStatusApproved,
		PaidAmountCents: 8000, // mismatch — 8000 != 9900
		TransactionNSU:  "tx-123",
	}}
	svc := newWebhookServiceForTest(orders, paymentsRepo, provider)

	raw := []byte(`{"transaction_id":"tx-123","order_id":"order-123","invoice_id":"inv-123"}`)
	err := svc.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected ErrAmountMismatch, got nil")
	}
	if !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("expected ErrAmountMismatch, got %v", err)
	}
	if paymentsRepo.upserts != 0 {
		t.Fatalf("expected no payment upsert on amount mismatch, got %d", paymentsRepo.upserts)
	}
}

// ─── Security fix: partial-failure propagation (Fix 2) ────────────────────────

// newWebhookServiceWith wires a WebhookService with all injected dependencies, used
// for focused partial-failure tests.
func newWebhookServiceWith(
	orders *fakeOrderRepo,
	paymentsRepo *fakePaymentRepo,
	outbox *fakeOutboxRepo,
	provider *fakeVerifyProvider,
) *WebhookService {
	return NewWebhookService(
		orders,
		paymentsRepo,
		fakeSubscriptionRepo{},
		&fakeWebhookEventRepo{},
		outbox,
		fakeWebhookIdempotencyStore{},
		fakeLockManager{},
		fakeStatusCache{},
		provider,
		zap.NewNop(),
	)
}

// TestWebhookService_Handle_OrderStatusUpdateFailure_PropagatesError verifies that
// a failure in Step 11 (UpdateStatus) is returned as an error rather than silently ignored.
// The provider must NOT receive a success response when durable order state was not applied.
func TestWebhookService_Handle_OrderStatusUpdateFailure_PropagatesError(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:          primitive.NewObjectID(),
		OrderNSU:    "order-123",
		AmountCents: 9900,
		Status:      string(OrderStatusCheckoutCreated),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	orders.updateStatusErr = errors.New("db write timeout")

	paymentsRepo := &fakePaymentRepo{}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status: PaymentStatusApproved, PaidAmountCents: 9900, TransactionNSU: "tx-123",
	}}
	svc := newWebhookServiceWith(orders, paymentsRepo, &fakeOutboxRepo{}, provider)

	raw := []byte(`{"transaction_id":"tx-123","order_id":"order-123","invoice_id":"inv-123"}`)
	err := svc.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error from UpdateStatus failure, got nil — partial failure must not be hidden")
	}
}

// TestWebhookService_Handle_OutboxInsertFailure_PropagatesError verifies that
// a transient failure in Step 12 (outbox.Insert) is returned as an error.
// The provider must NOT receive a success response when the outbox event was not durably enqueued.
func TestWebhookService_Handle_OutboxInsertFailure_PropagatesError(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:          primitive.NewObjectID(),
		OrderNSU:    "order-123",
		AmountCents: 9900,
		Status:      string(OrderStatusCheckoutCreated),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	paymentsRepo := &fakePaymentRepo{}
	outbox := &fakeOutboxRepo{insertErr: errors.New("mongo write concern failure")}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status: PaymentStatusApproved, PaidAmountCents: 9900, TransactionNSU: "tx-123",
	}}
	svc := newWebhookServiceWith(orders, paymentsRepo, outbox, provider)

	raw := []byte(`{"transaction_id":"tx-123","order_id":"order-123","invoice_id":"inv-123"}`)
	err := svc.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error from outbox.Insert failure, got nil — partial failure must not be hidden")
	}
}

// TestWebhookService_Handle_OutboxDuplicateInsert_TreatedAsSuccess verifies that
// when the outbox.Insert returns ErrDuplicateOutbox (idempotent retry scenario),
// Handle does NOT propagate the error — the event was already enqueued on a prior attempt.
func TestWebhookService_Handle_OutboxDuplicateInsert_TreatedAsSuccess(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:          primitive.NewObjectID(),
		OrderNSU:    "order-123",
		AmountCents: 9900,
		Status:      string(OrderStatusCheckoutCreated),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	paymentsRepo := &fakePaymentRepo{}
	outbox := &fakeOutboxRepo{insertErr: checkout.ErrDuplicateOutbox}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status: PaymentStatusApproved, PaidAmountCents: 9900, TransactionNSU: "tx-123",
	}}
	svc := newWebhookServiceWith(orders, paymentsRepo, outbox, provider)

	raw := []byte(`{"transaction_id":"tx-123","order_id":"order-123","invoice_id":"inv-123"}`)
	if err := svc.Handle(context.Background(), raw); err != nil {
		t.Fatalf("ErrDuplicateOutbox must be treated as success (idempotent retry), got error: %v", err)
	}
	if paymentsRepo.upserts != 1 {
		t.Fatalf("expected payment upsert to proceed, got %d", paymentsRepo.upserts)
	}
}

// ─── Story 4 invariant proofs ─────────────────────────────────────────────────

// TestWebhookService_Handle_SuccessPath_PersistedTransactionNSUIsProviderVerified is the
// Story 4 success-path invariant proof: the payment persisted by Handle must carry the
// provider-verified canonical transaction_nsu, not the webhook hint.
// This test explicitly asserts the upserted payment's TransactionNSU field.
func TestWebhookService_Handle_SuccessPath_PersistedTransactionNSUIsProviderVerified(t *testing.T) {
	const providerCanonicalID = "tx-provider-canonical"

	orders := newFakeOrderRepo()
	orders.orders["order-123"] = &checkout.Order{
		ID:            primitive.NewObjectID(),
		OrderNSU:      "order-123",
		PlanID:        "plan-basic",
		PlanSlug:      "basic",
		BillingCycle:  "monthly",
		AmountCents:   9900,
		CustomerEmail: "maria@example.com",
		Status:        string(OrderStatusCheckoutCreated),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	paymentsRepo := &fakePaymentRepo{}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status:          PaymentStatusApproved,
		PaidAmountCents: 9900,
		TransactionNSU:  providerCanonicalID,
		ReceiptURL:      "https://receipt.example/abc",
	}}
	svc := newWebhookServiceForTest(orders, paymentsRepo, provider)

	// Webhook hint equals provider-verified (so 6a passes). Story 4 proof: the persisted
	// transaction_nsu must be the provider-verified value — not sourced from the webhook hint.
	raw := []byte(`{"transaction_id":"tx-provider-canonical","order_id":"order-123","invoice_id":"inv-123"}`)
	if err := svc.Handle(context.Background(), raw); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if paymentsRepo.upserts != 1 {
		t.Fatalf("expected one payment upsert, got %d", paymentsRepo.upserts)
	}
	if len(paymentsRepo.upserted) == 0 {
		t.Fatal("fakePaymentRepo captured no upserted payment")
	}
	got := paymentsRepo.upserted[0].TransactionNSU
	if got != providerCanonicalID {
		t.Errorf("Story 4 invariant violated: persisted transaction_nsu = %q, want provider-verified %q", got, providerCanonicalID)
	}
}

// TestWebhookService_Handle_OrderNotFound_NoMutation proves that when the order is not found
// (Step 5), Handle returns an error with no payment upsert, no outbox insert, and no provider
// verify call. No durable state may be mutated for an unknown order.
func TestWebhookService_Handle_OrderNotFound_NoMutation(t *testing.T) {
	orders := newFakeOrderRepo() // empty — "order-unknown" does not exist
	paymentsRepo := &fakePaymentRepo{}
	outbox := &fakeOutboxRepo{}
	provider := &fakeVerifyProvider{response: InfinitePayVerifyResponse{
		Status:          PaymentStatusApproved,
		PaidAmountCents: 9900,
		TransactionNSU:  "tx-123",
	}}
	svc := newWebhookServiceWith(orders, paymentsRepo, outbox, provider)

	raw := []byte(`{"transaction_id":"tx-123","order_id":"order-unknown","invoice_id":"inv-123"}`)
	err := svc.Handle(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for unknown order, got nil")
	}
	// No mutation permitted when order is not found.
	if paymentsRepo.upserts != 0 {
		t.Errorf("expected no payment upsert on order-not-found, got %d", paymentsRepo.upserts)
	}
	if outbox.inserts != 0 {
		t.Errorf("expected no outbox insert on order-not-found, got %d", outbox.inserts)
	}
	// Provider verify must NOT be called before a valid order is confirmed (order load is Step 5,
	// verify is Step 6 — but if order lookup fails, verify should not have been called).
	if provider.verifyCalls != 0 {
		t.Errorf("expected no provider verify call on order-not-found, got %d", provider.verifyCalls)
	}
}
