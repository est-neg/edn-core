# InfinitePay TDD Plan

## Scope

This note defines the first test suite for a new `internal/payments` module that owns:

- checkout session creation
- InfinitePay webhook ingestion
- payment persistence and event publication
- subscription activation from `payment.approved`
- order status query
- admin payment verification and reconciliation

The current repository has no existing payments, Redis, worker, or outbox abstractions. The safest TDD path is to introduce the ports first, then land tests in `internal/payments` using the same constructor-injected style already used by `internal/leads`.

## Assumptions

- Module path remains `github.com/villenneve/vil-core`.
- New capability lands under `internal/payments`.
- MongoDB is the durable read/write store for payments, orders, webhook events, subscriptions, and outbox records for this slice.
- Redis is used only for ephemeral dedupe, status cache, and short-lived locks.
- InfinitePay integration stays behind an adapter port. Channel-specific HTTP details remain outside business rules.
- Event publication uses an outbox fallback when the publisher fails.

## Proposed Test File Structure

Exact new test files for the first slice:

- `internal/payments/testhelpers_test.go`
- `internal/payments/helpers_test.go`
- `internal/payments/request_validation_test.go`
- `internal/payments/checkout_service_test.go`
- `internal/payments/webhook_processor_test.go`
- `internal/payments/subscription_worker_test.go`
- `internal/payments/status_service_test.go`
- `internal/payments/admin_verification_service_test.go`
- `internal/payments/handler_test.go`
- `internal/payments/infinitepay_adapter_test.go`
- `internal/payments/integration_checkout_test.go`
- `internal/payments/integration_webhook_test.go`
- `internal/payments/integration_subscription_test.go`

Supporting production files that the tests assume exist:

- `internal/payments/types.go`
- `internal/payments/errors.go`
- `internal/payments/ports.go`
- `internal/payments/service.go`
- `internal/payments/webhook.go`
- `internal/payments/worker.go`
- `internal/payments/handler.go`
- `internal/payments/infinitepay_adapter.go`
- `internal/payments/mongodb_repository.go`

## Ports To Define First

These are the exact interface names the tests below assume are constructor-injected from `internal/payments/ports.go`:

```go
package payments

import (
    "context"
    "time"
)

type PlanRepository interface {
    FindActiveByCode(ctx context.Context, code string) (Plan, error)
}

type OrderRepository interface {
    Create(ctx context.Context, order Order) error
    GetByNSU(ctx context.Context, orderNSU string) (Order, error)
    UpdateCheckout(ctx context.Context, orderNSU string, checkout CheckoutSession) error
    UpdateStatus(ctx context.Context, orderNSU string, status OrderStatus, paymentID string) error
    GetStatus(ctx context.Context, orderNSU string) (OrderStatusView, error)
}

type PaymentRepository interface {
    UpsertByProviderPaymentID(ctx context.Context, payment Payment) (PaymentUpsertResult, error)
    GetByProviderPaymentID(ctx context.Context, providerPaymentID string) (Payment, error)
    GetByOrderNSU(ctx context.Context, orderNSU string) (Payment, error)
}

type WebhookEventRepository interface {
    SaveRaw(ctx context.Context, event WebhookEvent) error
    ExistsByHash(ctx context.Context, eventHash string) (bool, error)
    MarkProcessed(ctx context.Context, eventHash string, processedAt time.Time) error
}

type SubscriptionRepository interface {
    ActivateFromPayment(ctx context.Context, input SubscriptionActivation) (SubscriptionActivationResult, error)
    GetByOrderNSU(ctx context.Context, orderNSU string) (Subscription, error)
}

type OutboxRepository interface {
    Save(ctx context.Context, event OutboxEvent) error
}

type PaymentEventPublisher interface {
    PublishPaymentApproved(ctx context.Context, event PaymentApprovedEvent) error
}

type IdempotencyStore interface {
    Seen(ctx context.Context, key string) (bool, error)
    MarkSeen(ctx context.Context, key string, ttl time.Duration) error
}

type LockManager interface {
    Acquire(ctx context.Context, key string, ttl time.Duration) (Lock, error)
}

type Lock interface {
    Release(ctx context.Context) error
}

type StatusCache interface {
    GetOrderStatus(ctx context.Context, orderNSU string) (OrderStatusView, bool, error)
    SetOrderStatus(ctx context.Context, view OrderStatusView, ttl time.Duration) error
}

type InfinitePayClient interface {
    CreateCheckout(ctx context.Context, req InfinitePayCheckoutRequest) (InfinitePayCheckoutResponse, error)
    VerifyPayment(ctx context.Context, providerPaymentID string) (InfinitePayPaymentResponse, error)
}

type Clock interface {
    Now() time.Time
}

type IDGenerator interface {
    NewOrderNSU() string
}
```

Mocks needed for unit tests:

- `mockPlanRepository`
- `mockOrderRepository`
- `mockPaymentRepository`
- `mockWebhookEventRepository`
- `mockSubscriptionRepository`
- `mockOutboxRepository`
- `mockPaymentEventPublisher`
- `mockIdempotencyStore`
- `mockLockManager`
- `mockLock`
- `mockStatusCache`
- `mockInfinitePayClient`
- `mockClock`
- `mockIDGenerator`

## Test Helper Skeleton

Put shared fakes and builders in `internal/payments/testhelpers_test.go`.

```go
package payments_test

import (
    "context"
    "sync"
    "time"

    "go.uber.org/zap"

    "github.com/villenneve/vil-core/internal/payments"
)

type mockPlanRepository struct {
    plan payments.Plan
    err  error
    code string
}

func (m *mockPlanRepository) FindActiveByCode(_ context.Context, code string) (payments.Plan, error) {
    m.code = code
    return m.plan, m.err
}

type mockOrderRepository struct {
    mu              sync.Mutex
    created         []payments.Order
    checkoutUpdates []string
    statusUpdates   []payments.OrderStatus
    statusView      payments.OrderStatusView
    order           payments.Order
    err             error
}

func (m *mockOrderRepository) Create(_ context.Context, order payments.Order) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.err != nil {
        return m.err
    }
    m.created = append(m.created, order)
    return nil
}

func newCheckoutService(
    t *testing.T,
    plans payments.PlanRepository,
    orders payments.OrderRepository,
    provider payments.InfinitePayClient,
    clock payments.Clock,
    ids payments.IDGenerator,
) *payments.CheckoutService {
    t.Helper()
    return payments.NewCheckoutService(plans, orders, provider, clock, ids, zap.NewNop())
}
```

Keep these fakes tiny and deterministic. Add only behavior each test needs.

## Unit Test Skeletons

### `internal/payments/helpers_test.go`

```go
package payments_test

import (
    "regexp"
    "testing"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestNormalizePhone(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {name: "already normalized", input: "5511999990000", want: "5511999990000"},
        {name: "strips spaces and punctuation", input: "+55 (11) 99999-0000", want: "5511999990000"},
        {name: "trims surrounding whitespace", input: "  11999990000  ", want: "11999990000"},
        {name: "rejects empty", input: "", wantErr: true},
        {name: "rejects letters", input: "11ABC9990000", wantErr: true},
        {name: "rejects too short", input: "11999", wantErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := payments.NormalizePhone(tt.input)
            if tt.wantErr {
                if err == nil {
                    t.Fatal("expected error, got nil")
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if got != tt.want {
                t.Fatalf("expected %q, got %q", tt.want, got)
            }
        })
    }
}

func TestNewOrderNSU(t *testing.T) {
    tests := []struct {
        name      string
        iterations int
    }{
        {name: "single value matches uuid or ulid format", iterations: 1},
        {name: "multiple values remain unique", iterations: 256},
    }

    pattern := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$|^[a-f0-9-]{36}$`)

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            seen := make(map[string]struct{}, tt.iterations)
            for i := 0; i < tt.iterations; i++ {
                got := payments.NewOrderNSU()
                if !pattern.MatchString(got) {
                    t.Fatalf("unexpected NSU format: %q", got)
                }
                if _, exists := seen[got]; exists {
                    t.Fatalf("duplicate NSU generated: %q", got)
                }
                seen[got] = struct{}{}
            }
        })
    }
}

func TestCalculateEventHash(t *testing.T) {
    tests := []struct {
        name string
        body string
        want string
    }{
        {name: "empty body", body: "", want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
        {name: "stable hash for approved payload", body: `{"event":"payment.approved","id":"evt_1"}`},
        {name: "whitespace changes hash", body: "{\n  \"event\":\"payment.approved\"\n}"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := payments.CalculateEventHash([]byte(tt.body))
            if tt.want != "" && got != tt.want {
                t.Fatalf("expected %q, got %q", tt.want, got)
            }
            if got == "" {
                t.Fatal("expected non-empty hash")
            }
        })
    }
}
```

### `internal/payments/request_validation_test.go`

```go
package payments_test

import (
    "errors"
    "testing"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestCreateCheckoutRequest_Validate(t *testing.T) {
    tests := []struct {
        name    string
        mutate  func(*payments.CreateCheckoutRequest)
        wantErr bool
    }{
        {name: "valid request"},
        {name: "missing plan code", mutate: func(req *payments.CreateCheckoutRequest) { req.PlanCode = "" }, wantErr: true},
        {name: "missing customer name", mutate: func(req *payments.CreateCheckoutRequest) { req.CustomerName = "" }, wantErr: true},
        {name: "invalid email", mutate: func(req *payments.CreateCheckoutRequest) { req.CustomerEmail = "invalid" }, wantErr: true},
        {name: "invalid phone", mutate: func(req *payments.CreateCheckoutRequest) { req.CustomerPhone = "11-ABC" }, wantErr: true},
        {name: "zero amount rejected when caller overrides", mutate: func(req *payments.CreateCheckoutRequest) { req.AmountCents = 0 }, wantErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req := payments.CreateCheckoutRequest{
                PlanCode:      "pro-monthly",
                CustomerName:  "Maria Lima",
                CustomerEmail: "maria@example.com",
                CustomerPhone: "+55 (11) 99999-0000",
                AmountCents:   4990,
            }
            if tt.mutate != nil {
                tt.mutate(&req)
            }

            err := req.Validate()
            if tt.wantErr {
                if err == nil {
                    t.Fatal("expected error, got nil")
                }
                if !errors.Is(err, payments.ErrInvalidPayload) {
                    t.Fatalf("expected ErrInvalidPayload, got %v", err)
                }
                return
            }

            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
        })
    }
}
```

### `internal/payments/infinitepay_adapter_test.go`

```go
package payments_test

import (
    "testing"
    "time"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestMapInfinitePayPaymentResponse(t *testing.T) {
    tests := []struct {
        name    string
        input   payments.InfinitePayPaymentResponse
        want    payments.Payment
        wantErr bool
    }{
        {
            name: "approved maps to paid",
            input: payments.InfinitePayPaymentResponse{
                PaymentID:   "pay_123",
                OrderNSU:    "ord_123",
                Status:      "approved",
                AmountCents: 4990,
                PaidAt:      time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC),
            },
            want: payments.Payment{
                ProviderPaymentID: "pay_123",
                OrderNSU:          "ord_123",
                Status:            payments.PaymentStatusPaid,
                AmountCents:       4990,
            },
        },
        {
            name: "pending maps to pending",
            input: payments.InfinitePayPaymentResponse{
                PaymentID:   "pay_124",
                OrderNSU:    "ord_124",
                Status:      "pending",
                AmountCents: 4990,
            },
            want: payments.Payment{
                ProviderPaymentID: "pay_124",
                OrderNSU:          "ord_124",
                Status:            payments.PaymentStatusPending,
                AmountCents:       4990,
            },
        },
        {name: "missing provider payment id returns error", input: payments.InfinitePayPaymentResponse{OrderNSU: "ord_125", Status: "approved", AmountCents: 4990}, wantErr: true},
        {name: "unknown provider status returns error", input: payments.InfinitePayPaymentResponse{PaymentID: "pay_126", OrderNSU: "ord_126", Status: "mystery", AmountCents: 4990}, wantErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := payments.MapInfinitePayPayment(tt.input)
            if tt.wantErr {
                if err == nil {
                    t.Fatal("expected error, got nil")
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if got.ProviderPaymentID != tt.want.ProviderPaymentID {
                t.Fatalf("expected ProviderPaymentID %q, got %q", tt.want.ProviderPaymentID, got.ProviderPaymentID)
            }
            if got.Status != tt.want.Status {
                t.Fatalf("expected Status %q, got %q", tt.want.Status, got.Status)
            }
        })
    }
}
```

### `internal/payments/checkout_service_test.go`

```go
package payments_test

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestCheckoutService_CreateSession(t *testing.T) {
    tests := []struct {
        name               string
        plan               payments.Plan
        planErr            error
        providerResp       payments.InfinitePayCheckoutResponse
        providerErr        error
        orderRepoErr       error
        wantErr            error
        wantCheckoutURL    string
        wantOrderCreated   bool
        wantProviderCalled bool
        wantPhone          string
    }{
        {
            name: "active plan returns checkout url",
            plan: payments.Plan{Code: "pro-monthly", Active: true, AmountCents: 4990, Interval: "monthly"},
            providerResp: payments.InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/session/123", ProviderCheckoutID: "chk_123"},
            wantCheckoutURL:    "https://checkout.example/session/123",
            wantOrderCreated:   true,
            wantProviderCalled: true,
            wantPhone:          "5511999990000",
        },
        {
            name:    "inactive plan returns validation error",
            plan:    payments.Plan{Code: "pro-monthly", Active: false, AmountCents: 4990},
            wantErr: payments.ErrInactivePlan,
            wantOrderCreated:   false,
            wantProviderCalled: false,
        },
        {
            name:            "provider timeout keeps created order and returns error",
            plan:            payments.Plan{Code: "pro-monthly", Active: true, AmountCents: 4990},
            providerErr:     context.DeadlineExceeded,
            wantErr:         context.DeadlineExceeded,
            wantOrderCreated: true,
            wantProviderCalled: true,
        },
        {
            name:         "order create failure short circuits provider call",
            plan:         payments.Plan{Code: "pro-monthly", Active: true, AmountCents: 4990},
            orderRepoErr: errors.New("insert failed"),
            wantErr:      errors.New("insert failed"),
            wantProviderCalled: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            planRepo := &mockPlanRepository{plan: tt.plan, err: tt.planErr}
            orderRepo := &mockOrderRepository{err: tt.orderRepoErr}
            provider := &mockInfinitePayClient{checkoutResp: tt.providerResp, checkoutErr: tt.providerErr}
            clock := &mockClock{now: time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)}
            ids := &mockIDGenerator{orderNSU: "01JS9V6R0R5Y2W6P2H5QWE7ABC"}

            svc := newCheckoutService(t, planRepo, orderRepo, provider, clock, ids)
            resp, err := svc.CreateSession(context.Background(), payments.CreateCheckoutRequest{
                PlanCode:      "pro-monthly",
                CustomerName:  "Maria Lima",
                CustomerEmail: "maria@example.com",
                CustomerPhone: "+55 (11) 99999-0000",
            })

            if tt.wantErr != nil {
                if err == nil {
                    t.Fatal("expected error, got nil")
                }
                if !errors.Is(err, tt.wantErr) && err.Error() != tt.wantErr.Error() {
                    t.Fatalf("expected error %v, got %v", tt.wantErr, err)
                }
            } else if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }

            if tt.wantCheckoutURL != "" && resp.CheckoutURL != tt.wantCheckoutURL {
                t.Fatalf("expected checkout url %q, got %q", tt.wantCheckoutURL, resp.CheckoutURL)
            }
            if got := len(orderRepo.created) > 0; got != tt.wantOrderCreated {
                t.Fatalf("expected order created=%v, got %v", tt.wantOrderCreated, got)
            }
            if provider.checkoutCalled != tt.wantProviderCalled {
                t.Fatalf("expected provider called=%v, got %v", tt.wantProviderCalled, provider.checkoutCalled)
            }
            if tt.wantPhone != "" && provider.checkoutReq.CustomerPhone != tt.wantPhone {
                t.Fatalf("expected normalized phone %q, got %q", tt.wantPhone, provider.checkoutReq.CustomerPhone)
            }
        })
    }
}
```

### `internal/payments/webhook_processor_test.go`

```go
package payments_test

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestWebhookProcessor_Process(t *testing.T) {
    tests := []struct {
        name                  string
        body                  []byte
        redisSeen             bool
        redisSeenErr          error
        mongoSeen             bool
        mongoSeenErr          error
        lockErr               error
        paymentUpsertResult   payments.PaymentUpsertResult
        paymentUpsertErr      error
        publishErr            error
        amountCents           int64
        expectedAmountCents   int64
        wantErr               bool
        wantPublished         bool
        wantOutboxPersisted   bool
        wantSubscriptionEvent bool
        wantSkip              bool
        wantStatus            payments.PaymentStatus
    }{
        {
            name:                "approved webhook creates payment and publishes event",
            body:                []byte(`{"event":"payment.approved","payment_id":"pay_1","order_nsu":"ord_1","amount_cents":4990}`),
            amountCents:         4990,
            expectedAmountCents: 4990,
            paymentUpsertResult: payments.PaymentUpsertResult{Inserted: true},
            wantPublished:       true,
            wantStatus:          payments.PaymentStatusPaid,
        },
        {
            name:     "duplicate event hash in redis skips",
            body:     []byte(`{"event":"payment.approved","payment_id":"pay_1","order_nsu":"ord_1","amount_cents":4990}`),
            redisSeen: true,
            wantSkip:  true,
        },
        {
            name:                "redis unavailable falls back to mongo dedupe and continues",
            body:                []byte(`{"event":"payment.approved","payment_id":"pay_2","order_nsu":"ord_2","amount_cents":4990}`),
            redisSeenErr:        errors.New("redis unavailable"),
            amountCents:         4990,
            expectedAmountCents: 4990,
            paymentUpsertResult: payments.PaymentUpsertResult{Inserted: true},
            wantPublished:       true,
            wantStatus:          payments.PaymentStatusPaid,
        },
        {
            name:                "amount divergence marks pending review and does not publish",
            body:                []byte(`{"event":"payment.approved","payment_id":"pay_3","order_nsu":"ord_3","amount_cents":3990}`),
            amountCents:         3990,
            expectedAmountCents: 4990,
            paymentUpsertResult: payments.PaymentUpsertResult{Inserted: true},
            wantPublished:       false,
            wantStatus:          payments.PaymentStatusPendingReview,
        },
        {
            name:                "publish failure persists outbox for retry",
            body:                []byte(`{"event":"payment.approved","payment_id":"pay_4","order_nsu":"ord_4","amount_cents":4990}`),
            amountCents:         4990,
            expectedAmountCents: 4990,
            paymentUpsertResult: payments.PaymentUpsertResult{Inserted: true},
            publishErr:          errors.New("pubsub unavailable"),
            wantOutboxPersisted: true,
            wantStatus:          payments.PaymentStatusPaid,
        },
        {
            name:      "duplicate hash in mongo skips when redis misses",
            body:      []byte(`{"event":"payment.approved","payment_id":"pay_5","order_nsu":"ord_5","amount_cents":4990}`),
            mongoSeen: true,
            wantSkip:  true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            webhooks := &mockWebhookEventRepository{exists: tt.mongoSeen, existsErr: tt.mongoSeenErr}
            paymentsRepo := &mockPaymentRepository{upsertResult: tt.paymentUpsertResult, upsertErr: tt.paymentUpsertErr}
            orders := &mockOrderRepository{order: payments.Order{OrderNSU: "ord_1", AmountCents: tt.expectedAmountCents}}
            idem := &mockIdempotencyStore{seen: tt.redisSeen, seenErr: tt.redisSeenErr}
            locks := &mockLockManager{acquireErr: tt.lockErr}
            publisher := &mockPaymentEventPublisher{publishErr: tt.publishErr}
            outbox := &mockOutboxRepository{}
            clock := &mockClock{now: time.Date(2026, 4, 22, 12, 5, 0, 0, time.UTC)}

            processor := payments.NewWebhookProcessor(webhooks, orders, paymentsRepo, idem, locks, publisher, outbox, clock, zap.NewNop())
            result, err := processor.Process(context.Background(), tt.body)

            if tt.wantErr && err == nil {
                t.Fatal("expected error, got nil")
            }
            if !tt.wantErr && err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if result.Skipped != tt.wantSkip {
                t.Fatalf("expected skipped=%v, got %v", tt.wantSkip, result.Skipped)
            }
            if publisher.published != tt.wantPublished {
                t.Fatalf("expected published=%v, got %v", tt.wantPublished, publisher.published)
            }
            if got := len(outbox.saved) > 0; got != tt.wantOutboxPersisted {
                t.Fatalf("expected outbox persisted=%v, got %v", tt.wantOutboxPersisted, got)
            }
            if tt.wantStatus != "" && paymentsRepo.saved.Status != tt.wantStatus {
                t.Fatalf("expected status %q, got %q", tt.wantStatus, paymentsRepo.saved.Status)
            }
        })
    }
}
```

### `internal/payments/subscription_worker_test.go`

```go
package payments_test

import (
    "context"
    "testing"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestSubscriptionWorker_HandlePaymentApproved(t *testing.T) {
    tests := []struct {
        name                string
        payment             payments.Payment
        activationResult    payments.SubscriptionActivationResult
        activationErr       error
        wantErr             bool
        wantActivated       bool
        wantDuplicateIgnore bool
    }{
        {
            name: "activates subscription for first approved payment",
            payment: payments.Payment{ProviderPaymentID: "pay_1", OrderNSU: "ord_1", Status: payments.PaymentStatusPaid},
            activationResult: payments.SubscriptionActivationResult{Activated: true},
            wantActivated: true,
        },
        {
            name: "duplicate event does not duplicate subscription",
            payment: payments.Payment{ProviderPaymentID: "pay_1", OrderNSU: "ord_1", Status: payments.PaymentStatusPaid},
            activationResult: payments.SubscriptionActivationResult{AlreadyActive: true},
            wantDuplicateIgnore: true,
        },
        {
            name: "non paid payment is ignored",
            payment: payments.Payment{ProviderPaymentID: "pay_2", OrderNSU: "ord_2", Status: payments.PaymentStatusPendingReview},
            wantActivated: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            subscriptions := &mockSubscriptionRepository{result: tt.activationResult, err: tt.activationErr}
            orders := &mockOrderRepository{}
            cache := &mockStatusCache{}
            worker := payments.NewSubscriptionWorker(subscriptions, orders, cache, zap.NewNop())

            result, err := worker.HandlePaymentApproved(context.Background(), payments.PaymentApprovedEvent{Payment: tt.payment})
            if tt.wantErr && err == nil {
                t.Fatal("expected error, got nil")
            }
            if !tt.wantErr && err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if result.Activated != tt.wantActivated {
                t.Fatalf("expected activated=%v, got %v", tt.wantActivated, result.Activated)
            }
            if result.AlreadyActive != tt.wantDuplicateIgnore {
                t.Fatalf("expected already_active=%v, got %v", tt.wantDuplicateIgnore, result.AlreadyActive)
            }
        })
    }
}
```

### `internal/payments/status_service_test.go`

```go
package payments_test

import (
    "context"
    "testing"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestStatusService_GetOrderStatus(t *testing.T) {
    tests := []struct {
        name          string
        cacheHit      bool
        cacheView     payments.OrderStatusView
        repoView      payments.OrderStatusView
        wantStatus    payments.OrderStatus
        wantSubActive bool
        wantRepoRead  bool
    }{
        {
            name:          "frontend polls before webhook arrives returns pending",
            repoView:      payments.OrderStatusView{OrderNSU: "ord_1", Status: payments.OrderStatusPending},
            wantStatus:    payments.OrderStatusPending,
            wantSubActive: false,
            wantRepoRead:  true,
        },
        {
            name:          "frontend polls after webhook before worker returns paid without active subscription",
            repoView:      payments.OrderStatusView{OrderNSU: "ord_1", Status: payments.OrderStatusPaid, SubscriptionActive: false},
            wantStatus:    payments.OrderStatusPaid,
            wantSubActive: false,
            wantRepoRead:  true,
        },
        {
            name:          "after activation returns paid and active subscription",
            cacheHit:      true,
            cacheView:     payments.OrderStatusView{OrderNSU: "ord_1", Status: payments.OrderStatusPaid, SubscriptionActive: true},
            wantStatus:    payments.OrderStatusPaid,
            wantSubActive: true,
            wantRepoRead:  false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            cache := &mockStatusCache{view: tt.cacheView, hit: tt.cacheHit}
            orders := &mockOrderRepository{statusView: tt.repoView}
            svc := payments.NewStatusService(orders, cache, zap.NewNop())

            got, err := svc.GetOrderStatus(context.Background(), "ord_1")
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if got.Status != tt.wantStatus {
                t.Fatalf("expected status %q, got %q", tt.wantStatus, got.Status)
            }
            if got.SubscriptionActive != tt.wantSubActive {
                t.Fatalf("expected subscription_active=%v, got %v", tt.wantSubActive, got.SubscriptionActive)
            }
            if orders.getStatusCalled != tt.wantRepoRead {
                t.Fatalf("expected repo read=%v, got %v", tt.wantRepoRead, orders.getStatusCalled)
            }
        })
    }
}
```

### `internal/payments/admin_verification_service_test.go`

```go
package payments_test

import (
    "context"
    "errors"
    "testing"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestAdminVerificationService_VerifyPayment(t *testing.T) {
    tests := []struct {
        name                string
        providerResp        payments.InfinitePayPaymentResponse
        providerErr         error
        paymentUpsertResult payments.PaymentUpsertResult
        publishErr          error
        wantErr             bool
        wantPublished       bool
        wantOutboxPersisted bool
        wantStatus          payments.PaymentStatus
    }{
        {
            name: "approved provider payment reconciles and publishes",
            providerResp: payments.InfinitePayPaymentResponse{PaymentID: "pay_1", OrderNSU: "ord_1", Status: "approved", AmountCents: 4990},
            paymentUpsertResult: payments.PaymentUpsertResult{Updated: true},
            wantPublished: true,
            wantStatus: payments.PaymentStatusPaid,
        },
        {
            name:        "provider verify failure returns error",
            providerErr: context.DeadlineExceeded,
            wantErr:     true,
        },
        {
            name: "publish failure persists outbox",
            providerResp: payments.InfinitePayPaymentResponse{PaymentID: "pay_2", OrderNSU: "ord_2", Status: "approved", AmountCents: 4990},
            paymentUpsertResult: payments.PaymentUpsertResult{Updated: true},
            publishErr: errors.New("pubsub unavailable"),
            wantOutboxPersisted: true,
            wantStatus: payments.PaymentStatusPaid,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            provider := &mockInfinitePayClient{verifyResp: tt.providerResp, verifyErr: tt.providerErr}
            paymentsRepo := &mockPaymentRepository{upsertResult: tt.paymentUpsertResult}
            publisher := &mockPaymentEventPublisher{publishErr: tt.publishErr}
            outbox := &mockOutboxRepository{}
            svc := payments.NewAdminVerificationService(provider, paymentsRepo, publisher, outbox, zap.NewNop())

            result, err := svc.VerifyPayment(context.Background(), "pay_1")
            if tt.wantErr && err == nil {
                t.Fatal("expected error, got nil")
            }
            if !tt.wantErr && err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if result.Published != tt.wantPublished {
                t.Fatalf("expected published=%v, got %v", tt.wantPublished, result.Published)
            }
            if got := len(outbox.saved) > 0; got != tt.wantOutboxPersisted {
                t.Fatalf("expected outbox persisted=%v, got %v", tt.wantOutboxPersisted, got)
            }
            if tt.wantStatus != "" && paymentsRepo.saved.Status != tt.wantStatus {
                t.Fatalf("expected status %q, got %q", tt.wantStatus, paymentsRepo.saved.Status)
            }
        })
    }
}
```

### `internal/payments/handler_test.go`

Use the same `httptest` style as `internal/leads/handler_test.go`.

```go
package payments_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/villenneve/vil-core/internal/payments"
)

func TestHandler_CreateCheckout(t *testing.T) {
    tests := []struct {
        name       string
        body       any
        serviceErr error
        wantCode   int
    }{
        {name: "success", body: validCheckoutBody(), wantCode: http.StatusOK},
        {name: "inactive plan", body: validCheckoutBody(), serviceErr: payments.ErrInactivePlan, wantCode: http.StatusUnprocessableEntity},
        {name: "invalid payload", body: map[string]any{"plan_code": ""}, serviceErr: payments.ErrInvalidPayload, wantCode: http.StatusBadRequest},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            h := newPaymentsHandler(t, &fakeCheckoutCreator{err: tt.serviceErr}, nil, nil, nil)
            body, _ := json.Marshal(tt.body)
            req := httptest.NewRequest(http.MethodPost, "/api/payments/checkout", bytes.NewReader(body))
            req.Header.Set("Content-Type", "application/json")
            rec := httptest.NewRecorder()

            h.ServeHTTP(rec, req)

            if rec.Code != tt.wantCode {
                t.Fatalf("expected %d, got %d", tt.wantCode, rec.Code)
            }
        })
    }
}

func TestHandler_ProcessWebhook(t *testing.T) {
    tests := []struct {
        name       string
        body       string
        processErr error
        wantCode   int
    }{
        {name: "approved event accepted", body: `{"event":"payment.approved"}`, wantCode: http.StatusOK},
        {name: "duplicate event still returns 200", body: `{"event":"payment.approved"}`, processErr: payments.ErrDuplicateWebhookEvent, wantCode: http.StatusOK},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            h := newPaymentsHandler(t, nil, &fakeWebhookProcessor{err: tt.processErr}, nil, nil)
            req := httptest.NewRequest(http.MethodPost, "/api/payments/webhooks/infinitepay", bytes.NewBufferString(tt.body))
            rec := httptest.NewRecorder()

            h.ServeHTTP(rec, req)

            if rec.Code != tt.wantCode {
                t.Fatalf("expected %d, got %d", tt.wantCode, rec.Code)
            }
        })
    }
}
```

## Integration Test Approach

These tests should be contract-oriented and deterministic. Do not start with real MongoDB or Redis in CI for this first slice because the repository has no testcontainer or embedded infrastructure harness today.

### Doubles

#### Fake MongoDB repositories

Implement in-memory repositories backed by maps and mutexes for:

- plans
- orders
- payments
- webhook events by `event_hash`
- subscriptions
- outbox events

The fake should enforce the same business invariants the production repository will rely on:

- unique `order_nsu`
- unique `provider_payment_id`
- unique `event_hash`
- idempotent `ActivateFromPayment`

#### Fake Redis

Implement one in-memory fake that satisfies:

- `IdempotencyStore`
- `StatusCache`
- `LockManager`

Required behaviors:

- return `seen=true` for duplicate event hash
- simulate `redis unavailable` on read/write
- simulate cache hit and cache miss
- enforce a single lock holder per key

#### Fake InfinitePay server

Use `httptest.NewServer` for adapter integration tests.

Scenarios to serve:

- checkout creation success with `checkout_url`
- checkout creation timeout by blocking until request context is canceled
- verify payment success returning approved payload
- verify payment returning pending payload

### Real vs Fake Matrix

`integration_checkout_test.go`

- real: HTTP handler, JSON decode/encode, request validation, checkout service, InfinitePay adapter HTTP client
- fake: plan repository, order repository, status cache, clock, ID generator, InfinitePay server

`integration_webhook_test.go`

- real: webhook handler, raw body capture, `event_hash` calculation, webhook processor orchestration
- fake: webhook repository, payment repository, order repository, Redis dedupe/lock, publisher, outbox

`integration_subscription_test.go`

- real: subscription worker, order status service
- fake: payment repository, subscription repository, order repository, status cache

### Integration Test Cases To Implement

`internal/payments/integration_checkout_test.go`

```go
func TestIntegration_CreateSession_ActivePlanReturnsCheckoutURL(t *testing.T)
func TestIntegration_CreateSession_InactivePlanReturns422(t *testing.T)
func TestIntegration_CreateSession_ProviderTimeoutLeavesOrderCreated(t *testing.T)
```

`internal/payments/integration_webhook_test.go`

```go
func TestIntegration_ProcessWebhook_ApprovedCreatesPaymentAndPublishes(t *testing.T)
func TestIntegration_ProcessWebhook_DuplicateReturns200WithoutDuplicatePayment(t *testing.T)
func TestIntegration_ProcessWebhook_AmountDivergenceMarksPendingReviewWithoutSubscription(t *testing.T)
func TestIntegration_ProcessWebhook_RedisUnavailableFallsBackToMongoDedupe(t *testing.T)
func TestIntegration_ProcessWebhook_PublishFailurePersistsOutbox(t *testing.T)
```

`internal/payments/integration_subscription_test.go`

```go
func TestIntegration_ActivateSubscription_FromPaymentApprovedEvent(t *testing.T)
func TestIntegration_ActivateSubscription_DuplicateEventDoesNotDuplicateSubscription(t *testing.T)
func TestIntegration_QueryOrderStatus_AfterActivationReturnsPaidAndActive(t *testing.T)
func TestIntegration_QueryOrderStatus_BeforeWebhookReturnsPending(t *testing.T)
func TestIntegration_QueryOrderStatus_AfterWebhookBeforeWorkerReturnsPaidOrPendingWithoutActiveSubscription(t *testing.T)
```

## Acceptance Criteria Checklist

### 1. Checkout session creation

- [ ] Active plan lookup succeeds only for active plans.
- [ ] Customer phone is normalized before persistence and before the provider call.
- [ ] `order_nsu` is generated server-side and is unique.
- [ ] Order is persisted before the InfinitePay adapter call.
- [ ] Successful provider response returns `checkout_url` and stores provider checkout metadata.
- [ ] Provider timeout returns an error without deleting the created order.
- [ ] Inactive plan returns HTTP `422`.

### 2. Webhook processing

- [ ] Raw webhook body is persisted for audit/debug purposes.
- [ ] `event_hash` is SHA256 of the exact raw body bytes.
- [ ] Duplicate `event_hash` in Redis returns `200` and skips duplicate work.
- [ ] Redis failure falls back to MongoDB dedupe and still processes the event.
- [ ] Per-event lock is acquired before upsert/publish work starts.
- [ ] Payment is upserted by provider payment ID, not inserted blindly.
- [ ] Approved payment publishes `payment.approved` exactly once per new approval.
- [ ] Publish failure persists an outbox record so retry remains possible.
- [ ] Amount divergence sets payment status to `pending_review` and does not publish activation work.

### 3. Subscription activation worker

- [ ] Worker consumes `payment.approved` and activates the subscription exactly once.
- [ ] Duplicate events do not create duplicate subscriptions.
- [ ] Activation is idempotent when the subscription is already active.
- [ ] Successful activation updates order status/cache view.

### 4. Order status query

- [ ] Before webhook arrival, order status is `pending`.
- [ ] After payment approval but before worker completion, status is `pending` or `paid`, but subscription is not active yet.
- [ ] After worker activation, status returns `paid` with active subscription state.
- [ ] Redis cache hit returns current status without requiring MongoDB read.
- [ ] Cache miss repopulates Redis from MongoDB view.

### 5. Admin payment verification

- [ ] Admin endpoint triggers `VerifyPayment` through the InfinitePay adapter.
- [ ] Provider response is reconciled into the local payment record.
- [ ] Approved verification publishes `payment.approved` or persists outbox on publish failure.
- [ ] Verification path is safe to retry and remains idempotent.

## Test-First Implementation Order

1. `TestNormalizePhone`
2. `TestCalculateEventHash`
3. `TestNewOrderNSU`
4. `TestCreateCheckoutRequest_Validate`
5. `TestCheckoutService_CreateSession` with only the `active plan returns checkout url` case enabled first
6. `TestCheckoutService_CreateSession` inactive-plan and provider-timeout cases
7. `TestIntegration_CreateSession_ActivePlanReturnsCheckoutURL`
8. `TestWebhookProcessor_Process` with `approved webhook creates payment and publishes event`
9. `TestWebhookProcessor_Process` duplicate-hash, Redis-fallback, amount-divergence, and publish-failure cases
10. `TestIntegration_ProcessWebhook_ApprovedCreatesPaymentAndPublishes`
11. `TestSubscriptionWorker_HandlePaymentApproved`
12. `TestIntegration_ActivateSubscription_FromPaymentApprovedEvent`
13. `TestStatusService_GetOrderStatus`
14. `TestIntegration_QueryOrderStatus_AfterActivationReturnsPaidAndActive`
15. `TestAdminVerificationService_VerifyPayment`
16. `TestHandler_CreateCheckout` and `TestHandler_ProcessWebhook`

Rationale:

- Steps 1-4 establish deterministic domain helpers and trust-boundary validation.
- Steps 5-7 lock the public checkout behavior and the persistence-before-provider invariant.
- Steps 8-10 drive the most failure-prone workflow: idempotent webhook ingestion with auditability and retry semantics.
- Steps 11-14 prove asynchronous activation and user-visible status transitions.
- Steps 15-16 close the admin reconciliation and HTTP contract surface.

## Coverage Gaps To Track Explicitly

- Webhook signature verification is not covered in this plan because the current spec only mentions raw body save and dedupe. Add it if InfinitePay exposes a signed callback contract.
- No real MongoDB or Redis contract tests are included in the first slice. Add them only after the module shape stabilizes or CI introduces a repeatable harness.
- Index, TTL, and retention behavior for webhook raw bodies and outbox records still need a separate data note before repository implementation.

## QA Gate

Status: conditional pass.

Why conditional:

- The repository now has a precise TDD target for the new module.
- No executable payment tests exist yet, so readiness is not a full pass.
- The developer handoff should start by adding `internal/payments/ports.go` and making the first helper test fail.
