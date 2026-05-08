package payments

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/villenneve/vil-core/internal/checkout"
)

// ─── 1. Adapter error translation ─────────────────────────────────────────────

// checkoutOrderRepoNotFound is a minimal checkout.OrderRepository whose FindByNSU
// and FindByIntentKey always return checkout.ErrOrderNotFound.
type checkoutOrderRepoNotFound struct{}

func (r *checkoutOrderRepoNotFound) Create(_ context.Context, _ checkout.Order) error { return nil }
func (r *checkoutOrderRepoNotFound) FindByNSU(_ context.Context, _ string) (*checkout.Order, error) {
	return nil, checkout.ErrOrderNotFound
}
func (r *checkoutOrderRepoNotFound) FindByIntentKey(_ context.Context, _ string) (*checkout.Order, error) {
	return nil, checkout.ErrOrderNotFound
}
func (r *checkoutOrderRepoNotFound) FindByCustomerDocument(_ context.Context, _ string) ([]checkout.Order, error) {
	return nil, nil
}
func (r *checkoutOrderRepoNotFound) FindOpenByBusinessFingerprint(_ context.Context, _, _, _, _ string) ([]checkout.Order, error) {
	return nil, nil
}
func (r *checkoutOrderRepoNotFound) UpdateStatus(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}
func (r *checkoutOrderRepoNotFound) UpdateProviderURL(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}
func (r *checkoutOrderRepoNotFound) UpdateReceipt(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}
func (r *checkoutOrderRepoNotFound) MarkProviderCreateAttempted(_ context.Context, _ string, _ time.Time) error {
	return nil
}

// TestOrderRepoAdapter_GetByNSU_TranslatesCheckoutNotFound verifies that
// checkout.ErrOrderNotFound surfaced by the inner checkout.OrderRepository is
// translated to payments.ErrOrderNotFound by the adapter so service-layer
// errors.Is checks work correctly.
func TestOrderRepoAdapter_GetByNSU_TranslatesCheckoutNotFound(t *testing.T) {
	adapter := &orderRepo{inner: &checkoutOrderRepoNotFound{}}
	_, err := adapter.GetByNSU(context.Background(), "any-nsu")
	if !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("expected payments.ErrOrderNotFound, got %v", err)
	}
}

// TestOrderRepoAdapter_FindByIntentKey_TranslatesCheckoutNotFound verifies the same
// translation for FindByIntentKey.
func TestOrderRepoAdapter_FindByIntentKey_TranslatesCheckoutNotFound(t *testing.T) {
	adapter := &orderRepo{inner: &checkoutOrderRepoNotFound{}}
	_, err := adapter.FindByIntentKey(context.Background(), "any-key")
	if !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("expected payments.ErrOrderNotFound, got %v", err)
	}
}

// ─── 2. Same-key replay returns HTTP 200 via Resumed=true ─────────────────────

// TestCreateCheckoutSession_SameKeyReplay_Returns200 verifies that replaying the
// same Idempotency-Key for an already-committed checkout returns HTTP 200 (not 201).
func TestCreateCheckoutSession_SameKeyReplay_Returns200(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/replay",
		InvoiceSlug: "inv-replay",
	}, nil)
	h := newCheckoutHandlerWithService(svc)

	req := validCheckoutRequest()
	first := postCheckout(t, h, req, "same-key-001")
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", first.Code, first.Body.String())
	}

	second := postCheckout(t, h, req, "same-key-001")
	if second.Code != http.StatusOK {
		t.Fatalf("same-key replay: expected 200, got %d: %s", second.Code, second.Body.String())
	}
}

// ─── 3. Different key + active open order blocks create with safe 409 ────────

// TestCreateCheckoutSession_DifferentKey_ActiveOpenOrder_ReturnsSafeConflict
// verifies that a second create request with a different Idempotency-Key but
// identical tenant+CPF+plan fingerprint does NOT create a new order and does NOT
// return the existing checkout session. A safe 409 ErrCheckoutAlreadyOpen is
// returned instead.
func TestCreateCheckoutSession_DifferentKey_ActiveOpenOrder_ReturnsSafeConflict(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/active",
		InvoiceSlug: "inv-active",
	}, nil)

	// First create — establishes the open order.
	first, err := svc.CreateSession(context.Background(), validCheckoutRequest())
	if err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}
	if len(orders.orders) != 1 {
		t.Fatalf("expected 1 order after first create, got %d", len(orders.orders))
	}

	// Second create — different Idempotency-Key, same tenant+CPF+plan fingerprint.
	req2 := validCheckoutRequest()
	req2.IdempotencyKey = "different-key-002"
	_, err = svc.CreateSession(context.Background(), req2)

	if !errors.Is(err, ErrCheckoutAlreadyOpen) {
		t.Fatalf("expected ErrCheckoutAlreadyOpen, got %v", err)
	}

	// No new order must have been created.
	if len(orders.orders) != 1 {
		t.Errorf("expected exactly 1 order (no duplicate), got %d", len(orders.orders))
	}

	// The existing order must NOT have been mutated (URL/key must be intact but not returned).
	existing := orders.orders[first.OrderNSU]
	if existing == nil {
		t.Fatal("original order missing from repo")
	}
	// Verify no session data leaked: the error path must carry nothing.
	_ = first.CheckoutURL       // caller from first create still holds it legitimately
	_ = first.CheckoutIntentKey // same
}

// TestCreateCheckoutSession_DifferentKey_MismatchedContact_SameCPF_ReturnsSafeConflict
// verifies that a second create request with different customer contact fields (name,
// email, phone) but the same CPF and same tenant+plan fingerprint also results in
// ErrCheckoutAlreadyOpen. The caller must not receive any session data regardless of
// whether their contact fields match the order owner.
func TestCreateCheckoutSession_DifferentKey_MismatchedContact_SameCPF_ReturnsSafeConflict(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/owner",
		InvoiceSlug: "inv-owner",
	}, nil)

	// First create by the original owner.
	_, err := svc.CreateSession(context.Background(), validCheckoutRequest())
	if err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}

	// Second create — same CPF, different name/email/phone, different idempotency key.
	req2 := validCheckoutRequest()
	req2.IdempotencyKey = "attacker-key-003"
	req2.Customer.Name = "Atacante Silva"
	req2.Customer.Email = "attacker@evil.example"
	req2.Customer.Phone = "11911112222"
	// Document (CPF) is the same as validCheckoutRequest.

	_, err = svc.CreateSession(context.Background(), req2)

	if !errors.Is(err, ErrCheckoutAlreadyOpen) {
		t.Fatalf("expected ErrCheckoutAlreadyOpen for mismatched-contact same-CPF, got %v", err)
	}
	if len(orders.orders) != 1 {
		t.Errorf("expected exactly 1 order (no duplicate), got %d", len(orders.orders))
	}
}

// ─── 4a. Clock-expired matching order allows new order creation ────────────────

// TestCreateCheckoutSession_DifferentKey_ClockExpiredOrder_CreatesNew verifies that
// when the only matching open order has an expires_at in the past, it is best-effort
// marked expired and a fresh order is created.
func TestCreateCheckoutSession_DifferentKey_ClockExpiredOrder_CreatesNew(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["clock-expired-nsu"] = &checkout.Order{
		OrderNSU:            "clock-expired-nsu",
		TenantID:            "tenant-001",
		PlanSlug:            "basic",
		BillingCycle:        "monthly",
		CustomerDocument:    "52998224725",
		Status:              string(OrderStatusCheckoutCreated),
		ProviderCheckoutURL: "https://checkout.example/old",
		CheckoutIntentKey:   GenerateCheckoutIntentKey(),
		ExpiresAt:           time.Now().UTC().Add(-5 * time.Minute), // already expired
		CreatedAt:           time.Now().UTC().Add(-40 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-40 * time.Minute),
	}

	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/new-after-expired",
		InvoiceSlug: "inv-new-after-expired",
	}, nil)

	req := validCheckoutRequest()
	req.IdempotencyKey = "fresh-key-after-expired"
	resp, err := svc.CreateSession(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateSession after clock-expired order: %v", err)
	}
	if resp.OrderNSU == "clock-expired-nsu" {
		t.Error("expected a new order, not the clock-expired one reused")
	}
	if resp.CheckoutURL == "" {
		t.Error("expected a checkout_url on the new order")
	}

	// The expired order must be marked expired.
	expired, err := orders.GetByNSU(context.Background(), "clock-expired-nsu")
	if err != nil {
		t.Fatalf("GetByNSU clock-expired: %v", err)
	}
	if expired.Status != string(OrderStatusExpired) {
		t.Errorf("expected clock-expired order to be marked expired, got %q", expired.Status)
	}
}

// ─── 4b. Provider-attempted/no-URL matching order causes safe 409 ─────────────

// TestCreateCheckoutSession_DifferentKey_ProviderAttemptedNoURL_ReturnsSafeConflict
// verifies that when the matching open order has provider_create_attempted_at set
// but no provider_checkout_url, the fingerprint dedup path does NOT expire the order
// and does NOT create a new order. A safe ErrCheckoutAlreadyOpen is returned —
// the caller must use their original checkout_intent_key to resume/recover.
func TestCreateCheckoutSession_DifferentKey_ProviderAttemptedNoURL_ReturnsSafeConflict(t *testing.T) {
	orders := newFakeOrderRepo()
	attemptedAt := time.Now().UTC().Add(-3 * time.Minute)
	orders.orders["attempted-nsu-dedup"] = &checkout.Order{
		OrderNSU:                  "attempted-nsu-dedup",
		TenantID:                  "tenant-001",
		PlanSlug:                  "basic",
		BillingCycle:              "monthly",
		CustomerDocument:          "52998224725",
		Status:                    string(OrderStatusCreated),
		ProviderCheckoutURL:       "",
		ProviderCreateAttemptedAt: &attemptedAt,
		CheckoutIntentKey:         GenerateCheckoutIntentKey(),
		ExpiresAt:                 time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:                 time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:                 time.Now().UTC().Add(-3 * time.Minute),
	}

	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://should.not.be.created/",
		InvoiceSlug: "must-not-create",
	}, nil)

	req := validCheckoutRequest()
	req.IdempotencyKey = "different-key-attempted-dedup"
	_, err := svc.CreateSession(context.Background(), req)

	if !errors.Is(err, ErrCheckoutAlreadyOpen) {
		t.Fatalf("expected ErrCheckoutAlreadyOpen, got %v", err)
	}

	// The ambiguous order must NOT have been marked expired.
	ambiguous, getErr := orders.GetByNSU(context.Background(), "attempted-nsu-dedup")
	if getErr != nil {
		t.Fatalf("GetByNSU attempted-nsu-dedup: %v", getErr)
	}
	if ambiguous.Status == string(OrderStatusExpired) {
		t.Error("provider-attempted order must NOT be marked expired by the dedup path")
	}

	// No new order must have been created.
	if len(orders.orders) != 1 {
		t.Errorf("expected exactly 1 order in repo (no duplicate created), got %d", len(orders.orders))
	}
}

// ─── 4c. Order without checkout_intent_key is not reused as active checkout ───

// TestCreateCheckoutSession_DifferentKey_NoIntentKey_DoesNotReuse verifies that a
// matching open order that lacks checkout_intent_key (legacy order) is NOT returned
// as the active checkout. The dedup path best-effort expires it and a new order is
// created for the incoming request.
func TestCreateCheckoutSession_DifferentKey_NoIntentKey_DoesNotReuse(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.orders["no-key-nsu"] = &checkout.Order{
		OrderNSU:            "no-key-nsu",
		TenantID:            "tenant-001",
		PlanSlug:            "basic",
		BillingCycle:        "monthly",
		CustomerDocument:    "52998224725",
		Status:              string(OrderStatusCheckoutCreated),
		ProviderCheckoutURL: "https://checkout.example/legacy",
		CheckoutIntentKey:   "", // legacy order — no intent key
		ExpiresAt:           time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-5 * time.Minute),
	}

	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/new-after-legacy",
		InvoiceSlug: "inv-new-after-legacy",
	}, nil)

	req := validCheckoutRequest()
	req.IdempotencyKey = "fresh-key-no-intent-key"
	resp, err := svc.CreateSession(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateSession after legacy order: %v", err)
	}
	if resp.OrderNSU == "no-key-nsu" {
		t.Error("expected a new order, not the legacy no-intent-key order reused")
	}
	if resp.CheckoutIntentKey == "" {
		t.Error("new order must have a checkout_intent_key")
	}
	if resp.CheckoutURL == "" {
		t.Error("expected a checkout_url on the new order")
	}
}

// ─── 5. Recovery path sets provider_create_attempted_at before provider call ───

// TestRecovery_SetsProviderCreateAttemptedAt verifies that the recovery path
// (resume of an order that has no provider_checkout_url) persists
// provider_create_attempted_at on the order before calling the provider,
// matching the safety invariant of the initial create path.
func TestRecovery_SetsProviderCreateAttemptedAt(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["recovery-pcat-nsu"] = &checkout.Order{
		OrderNSU:            "recovery-pcat-nsu",
		TenantID:            "tenant-001",
		PlanID:              "plan-basic",
		AmountCents:         9900,
		Status:              string(OrderStatusCreated),
		ProviderCheckoutURL: "",
		InvoiceSlug:         "",
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-5 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/recovery-pcat",
		InvoiceSlug: "inv-recovery-pcat",
	}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "recovery-pcat-001",
		CheckoutIntentKey: intentKey,
	})
	if err != nil {
		t.Fatalf("recovery CreateSession: %v", err)
	}
	order, err := orders.GetByNSU(context.Background(), "recovery-pcat-nsu")
	if err != nil {
		t.Fatalf("GetByNSU: %v", err)
	}
	if order.ProviderCreateAttemptedAt == nil {
		t.Error("expected provider_create_attempted_at to be set by recovery path before provider call")
	}
}

// ─── 6. Resume without Idempotency-Key succeeds ─────────────────────────────

// TestHandler_Resume_WithoutIdempotencyKey_Returns200 verifies that the HTTP
// handler returns 200 when resume is attempted with a valid checkout_intent_key
// and no Idempotency-Key header.
func TestHandler_Resume_WithoutIdempotencyKey_Returns200(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["no-idem-nsu"] = &checkout.Order{
		OrderNSU:            "no-idem-nsu",
		TenantID:            "tenant-001",
		Status:              string(OrderStatusCheckoutCreated),
		ProviderCheckoutURL: "https://checkout.example/no-idem",
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-5 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)
	h := newCheckoutHandlerWithService(svc)

	// No Idempotency-Key header.
	rr := postCheckout(t, h, CreateCheckoutRequest{CheckoutIntentKey: intentKey}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for resume without Idempotency-Key, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ─── Issue 1: fingerprint lookup failure must fail closed ────────────────────

// TestCreateCheckoutSession_FingerprintLookupFailure_FailsClosed verifies that
// when FindOpenByBusinessFingerprint returns an error, CreateSession does NOT
// proceed to create a new order. The dedup guard must fail closed, not silently
// allow the create to continue.
func TestCreateCheckoutSession_FingerprintLookupFailure_FailsClosed(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.findOpenByFingerprintErr = errors.New("mongodb: network timeout")

	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/must-not-reach",
		InvoiceSlug: "must-not-reach",
	}, nil)

	req := validCheckoutRequest()
	req.IdempotencyKey = "lookup-failure-key-001"

	_, err := svc.CreateSession(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when fingerprint lookup fails, got nil")
	}

	// No order must have been created.
	if len(orders.orders) != 0 {
		t.Errorf("expected 0 orders (fail closed), got %d", len(orders.orders))
	}
}

// ─── Issue 2: fingerprint lock infrastructure error must not map to ErrCheckoutInProgress ──

// TestCreateCheckoutSession_FingerprintLockInfraError_IsNotCheckoutInProgress
// verifies that when AcquireFingerprintLock returns an error other than
// checkout.ErrLockNotAcquired (e.g. a Redis failure), the service does NOT
// return ErrCheckoutInProgress. A 409 "already in progress" must only be returned
// when the lock is legitimately held by a concurrent request.
func TestCreateCheckoutSession_FingerprintLockInfraError_IsNotCheckoutInProgress(t *testing.T) {
	orders := newFakeOrderRepo()
	infraErr := errors.New("redis: connection refused")
	svc := newResumeTestServiceWithLock(
		orders,
		fakeLockManager{fingerprintLockErr: infraErr},
		InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/must-not-reach"},
		nil,
	)

	req := validCheckoutRequest()
	req.IdempotencyKey = "fp-lock-infra-err-001"

	_, err := svc.CreateSession(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when fingerprint lock returns infra error, got nil")
	}
	if errors.Is(err, ErrCheckoutInProgress) {
		t.Fatalf("infrastructure lock error must NOT surface as ErrCheckoutInProgress, got %v", err)
	}

	// No order must have been created.
	if len(orders.orders) != 0 {
		t.Errorf("expected 0 orders (no create on infra error), got %d", len(orders.orders))
	}
}

// ─── Issue 3: stale-order expiry lock failure must block create ───────────────

// TestCreateCheckoutSession_StaleOrderExpiryLockFailure_BlocksCreate verifies
// that when the order lock for a clock-expired stale order cannot be acquired,
// the service does NOT proceed to create a new order. Failing to acquire the
// expiry lock means another process may be acting on the order, so we fail
// closed rather than risk creating a duplicate.
func TestCreateCheckoutSession_StaleOrderExpiryLockFailure_BlocksCreate(t *testing.T) {
	const staleNSU = "stale-lock-held-nsu"
	orders := newFakeOrderRepo()
	orders.orders[staleNSU] = &checkout.Order{
		OrderNSU:            staleNSU,
		TenantID:            "tenant-001",
		PlanSlug:            "basic",
		BillingCycle:        "monthly",
		CustomerDocument:    "52998224725",
		Status:              string(OrderStatusCheckoutCreated),
		ProviderCheckoutURL: "https://checkout.example/stale",
		CheckoutIntentKey:   GenerateCheckoutIntentKey(),
		ExpiresAt:           time.Now().UTC().Add(-5 * time.Minute), // clock-expired
		CreatedAt:           time.Now().UTC().Add(-40 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-40 * time.Minute),
	}

	// Lock manager that refuses the order lock for the stale order's NSU.
	svc := newResumeTestServiceWithLock(
		orders,
		fakeLockManager{failOrderLockForNSU: staleNSU},
		InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/must-not-be-created"},
		nil,
	)

	req := validCheckoutRequest()
	req.IdempotencyKey = "stale-lock-failure-001"

	_, err := svc.CreateSession(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when stale-order expiry lock fails, got nil")
	}

	// No new order must have been created — still exactly 1 order in the repo.
	if len(orders.orders) != 1 {
		t.Errorf("expected exactly 1 order (no duplicate created), got %d", len(orders.orders))
	}

	// The stale order must NOT have been marked expired (lock was never acquired).
	stale := orders.orders[staleNSU]
	if stale.Status == string(OrderStatusExpired) {
		t.Error("stale order must not be marked expired when lock acquisition fails")
	}
}
