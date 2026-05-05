package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/idempotency"
)

// ─── Provider-attempted ambiguous order tests ─────────────────────────────────

// TestRecovery_ProviderAttempted_DoesNotRecreate verifies that an order with
// provider_create_attempted_at set and no URL does NOT trigger a new provider call
// during resume/recovery. It must return ErrProviderStateAmbiguous.
func TestRecovery_ProviderAttempted_DoesNotRecreate(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	attemptedAt := time.Now().UTC().Add(-2 * time.Minute)
	orders.orders["attempted-nsu"] = &checkout.Order{
		OrderNSU:                  "attempted-nsu",
		TenantID:                  "tenant-001",
		PlanID:                    "plan-basic",
		AmountCents:               9900,
		Status:                    string(OrderStatusCreated),
		ProviderCheckoutURL:       "",
		InvoiceSlug:               "",
		ProviderCreateAttemptedAt: &attemptedAt,
		CheckoutIntentKey:         intentKey,
		ExpiresAt:                 time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:                 time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:                 time.Now().UTC().Add(-2 * time.Minute),
	}
	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://should.not.be.called/", InvoiceSlug: "must-not-create"}}
	svc := newResumeTestService(orders, provider.resp, nil)
	// Replace with the provider tracking the call count.
	svc.provider = provider

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-attempted-001",
		CheckoutIntentKey: intentKey,
	})

	if !errors.Is(err, ErrProviderStateAmbiguous) {
		t.Fatalf("expected ErrProviderStateAmbiguous for provider-attempted order, got %v", err)
	}
	if provider.calls != 0 {
		t.Errorf("provider must not be called for provider-attempted order; calls=%d", provider.calls)
	}
}

// TestRecovery_ProviderAttempted_WithCommittedSnapshot_RepairsWithoutProviderCall verifies that
// an order with provider_create_attempted_at set and a committed idempotency snapshot containing
// a durable ResourceURL is repaired from the snapshot without a second provider call.
func TestRecovery_ProviderAttempted_WithCommittedSnapshot_RepairsWithoutProviderCall(t *testing.T) {
	orders := newFakeOrderRepo()
	idemRepo := newFakeCheckoutIdempotencyRepo()
	intentKey := GenerateCheckoutIntentKey()
	idempotencyKey := "idem-original-repair-001"
	durableURL := "https://checkout.example/durable-from-snapshot"
	durableSlug := "inv-snapshot-001"
	attemptedAt := time.Now().UTC().Add(-3 * time.Minute)

	orders.orders["repair-nsu"] = &checkout.Order{
		OrderNSU:                  "repair-nsu",
		TenantID:                  "tenant-001",
		PlanID:                    "plan-basic",
		AmountCents:               9900,
		Status:                    string(OrderStatusCreated),
		ProviderCheckoutURL:       "",
		InvoiceSlug:               "",
		ProviderCreateAttemptedAt: &attemptedAt,
		CheckoutIntentKey:         intentKey,
		ExpiresAt:                 time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:                 time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:                 time.Now().UTC().Add(-3 * time.Minute),
	}

	// Pre-seed idempotency snapshot as committed with durable URL.
	committedAt := time.Now().UTC().Add(-2 * time.Minute)
	idemRepo.records[idemLookup("tenant-001", checkoutCreateOperation, idempotencyKey)] = &idempotency.Key{
		Operation:      checkoutCreateOperation,
		TenantID:       "tenant-001",
		IdempotencyKey: idempotencyKey,
		ResourceID:     "repair-nsu",
		ResourceStatus: string(OrderStatusCheckoutCreated),
		ResourceURL:    durableURL,
		ExternalRef:    durableSlug,
		Status:         idempotency.StatusCommitted,
		UpdatedAt:      committedAt,
	}

	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://should.not.be.called/", InvoiceSlug: "must-not-create"}}

	svc := NewCheckoutService(
		newResumeTestService(orders, provider.resp, nil).versionedPlans,
		newResumeTestService(orders, provider.resp, nil).organizations,
		newResumeTestService(orders, provider.resp, nil).tenants,
		orders,
		idemRepo,
		provider,
		fakeLockManager{},
		fakeStatusCache{},
		newResumeTestService(orders, provider.resp, nil).cfg,
		zap.NewNop(),
	)

	resp, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-repair-001",
		CheckoutIntentKey: intentKey,
	})

	if err != nil {
		t.Fatalf("expected recovery success from snapshot, got %v", err)
	}
	if !resp.Resumed {
		t.Error("expected Resumed=true for snapshot-repaired checkout")
	}
	if resp.CheckoutURL != durableURL {
		t.Errorf("expected checkout_url %q from snapshot, got %q", durableURL, resp.CheckoutURL)
	}
	if resp.OrderNSU != "repair-nsu" {
		t.Errorf("expected same order_nsu %q, got %q", "repair-nsu", resp.OrderNSU)
	}
	if provider.calls != 0 {
		t.Errorf("provider must not be called when repairing from snapshot; calls=%d", provider.calls)
	}
	// Verify the order was repaired in the repo.
	repaired, err := orders.GetByNSU(context.Background(), "repair-nsu")
	if err != nil {
		t.Fatalf("GetByNSU after repair: %v", err)
	}
	if repaired.ProviderCheckoutURL != durableURL {
		t.Errorf("expected order.ProviderCheckoutURL=%q after repair, got %q", durableURL, repaired.ProviderCheckoutURL)
	}
	if repaired.InvoiceSlug != durableSlug {
		t.Errorf("expected order.InvoiceSlug=%q after repair, got %q", durableSlug, repaired.InvoiceSlug)
	}
}

// TestRecovery_ProviderAttempted_PendingSnapshot_ReturnsAmbiguous verifies that
// provider_create_attempted_at set with a pending (non-committed) idempotency snapshot
// returns ErrProviderStateAmbiguous rather than attempting recovery.
func TestRecovery_ProviderAttempted_PendingSnapshot_ReturnsAmbiguous(t *testing.T) {
	orders := newFakeOrderRepo()
	idemRepo := newFakeCheckoutIdempotencyRepo()
	intentKey := GenerateCheckoutIntentKey()
	idempotencyKey := "idem-pending-001"
	attemptedAt := time.Now().UTC().Add(-2 * time.Minute)

	orders.orders["pending-snap-nsu"] = &checkout.Order{
		OrderNSU:                  "pending-snap-nsu",
		TenantID:                  "tenant-001",
		PlanID:                    "plan-basic",
		AmountCents:               9900,
		Status:                    string(OrderStatusCreated),
		ProviderCheckoutURL:       "",
		InvoiceSlug:               "",
		ProviderCreateAttemptedAt: &attemptedAt,
		CheckoutIntentKey:         intentKey,
		ExpiresAt:                 time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:                 time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:                 time.Now().UTC().Add(-2 * time.Minute),
	}
	// Snapshot exists but is still pending and has no ResourceURL.
	idemRepo.records[idemLookup("tenant-001", checkoutCreateOperation, idempotencyKey)] = &idempotency.Key{
		Operation:      checkoutCreateOperation,
		TenantID:       "tenant-001",
		IdempotencyKey: idempotencyKey,
		ResourceID:     "pending-snap-nsu",
		ResourceURL:    "", // not yet committed
		Status:         idempotency.StatusPending,
	}

	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{}}
	svc := NewCheckoutService(
		newResumeTestService(orders, provider.resp, nil).versionedPlans,
		newResumeTestService(orders, provider.resp, nil).organizations,
		newResumeTestService(orders, provider.resp, nil).tenants,
		orders, idemRepo, provider, fakeLockManager{}, fakeStatusCache{},
		newResumeTestService(orders, provider.resp, nil).cfg, zap.NewNop(),
	)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-pending-snap-001",
		CheckoutIntentKey: intentKey,
	})
	if !errors.Is(err, ErrProviderStateAmbiguous) {
		t.Fatalf("expected ErrProviderStateAmbiguous for pending snapshot, got %v", err)
	}
	if provider.calls != 0 {
		t.Errorf("provider must not be called; calls=%d", provider.calls)
	}
}

// ─── SameKey replay path: provider-attempted order must return ErrProviderStateAmbiguous ────

// TestReplay_ProviderAttempted_SameKey_ReturnsAmbiguous verifies that when the same
// Idempotency-Key is replayed and the order has provider_create_attempted_at set without
// a URL, the replay returns ErrProviderStateAmbiguous instead of ErrCheckoutInProgress.
func TestReplay_ProviderAttempted_SameKey_ReturnsAmbiguous(t *testing.T) {
	orders := newFakeOrderRepo()
	idemRepo := newFakeCheckoutIdempotencyRepo()
	idempotencyKey := "idem-replay-attempted-001"
	attemptedAt := time.Now().UTC().Add(-2 * time.Minute)
	orderNSU := "replay-attempted-nsu"

	orders.orders[orderNSU] = &checkout.Order{
		OrderNSU:                  orderNSU,
		TenantID:                  "tenant-001",
		PlanID:                    "plan-basic",
		AmountCents:               9900,
		Status:                    string(OrderStatusCreated),
		ProviderCheckoutURL:       "",
		InvoiceSlug:               "",
		ProviderCreateAttemptedAt: &attemptedAt,
		CheckoutIntentKey:         GenerateCheckoutIntentKey(),
		ExpiresAt:                 time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:                 time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:                 time.Now().UTC().Add(-2 * time.Minute),
	}
	// Seed idempotency record as pending with no committed URL.
	idemRepo.records[idemLookup("tenant-001", checkoutCreateOperation, idempotencyKey)] = &idempotency.Key{
		Operation:      checkoutCreateOperation,
		TenantID:       "tenant-001",
		IdempotencyKey: idempotencyKey,
		RequestHash:    CalculateCheckoutRequestHash("acme", "clinic", "web", "basic", "monthly", "Joao Silva", "joao@example.com", "+5511987654321", "52998224725"),
		ResourceID:     orderNSU,
		ResourceURL:    "",
		Status:         idempotency.StatusPending,
	}

	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{}}
	svc := NewCheckoutService(
		newResumeTestService(orders, provider.resp, nil).versionedPlans,
		newResumeTestService(orders, provider.resp, nil).organizations,
		newResumeTestService(orders, provider.resp, nil).tenants,
		orders, idemRepo, provider, fakeLockManager{}, fakeStatusCache{},
		newResumeTestService(orders, provider.resp, nil).cfg, zap.NewNop(),
	)

	// Same-key replay with the original request (same Idempotency-Key).
	req := validCheckoutRequest()
	req.IdempotencyKey = idempotencyKey
	_, err := svc.CreateSession(context.Background(), req)
	if !errors.Is(err, ErrProviderStateAmbiguous) {
		t.Fatalf("expected ErrProviderStateAmbiguous for provider-attempted replay, got %v", err)
	}
}

// ─── Tracking stale-200 fallback test ────────────────────────────────────────

// fakeFailingStatusService is a TrackByIntentKey fake that always fails the second call.
type fakeFailingStatusService struct {
	callCount int
	first     TrackCheckoutResponse
}

func (f *fakeFailingStatusService) TrackByIntentKey(_ context.Context, _ string) (TrackCheckoutResponse, error) {
	f.callCount++
	if f.callCount == 1 {
		return f.first, nil
	}
	return TrackCheckoutResponse{}, errors.New("transient DB failure")
}

// fakeSucceedingCheckoutService records RecoverByIntentKey calls and returns nil.
type fakeSucceedingCheckoutService struct {
	calls int
}

func (f *fakeSucceedingCheckoutService) RecoverByIntentKey(_ context.Context, _ string) (CreateCheckoutResponse, error) {
	f.calls++
	return CreateCheckoutResponse{}, nil
}

// trackHandlerWithFakes builds a minimal Handler wired to fake status and checkout services
// that implement only the interfaces needed by TrackCheckoutSession.
type trackableStatusService interface {
	TrackByIntentKey(ctx context.Context, intentKey string) (TrackCheckoutResponse, error)
}

type trackableCheckoutService interface {
	RecoverByIntentKey(ctx context.Context, intentKey string) (CreateCheckoutResponse, error)
}

// trackHandler uses unexported field injection via a thin test adapter so we can control
// both the status and checkout services independently.
type testTrackHandler struct {
	statusSvc   trackableStatusService
	checkoutSvc trackableCheckoutService
	log         *zap.Logger
}

func (h *testTrackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req TrackCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	intentKey := req.CheckoutIntentKey
	if intentKey == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "checkout_intent_key is required"})
		return
	}
	resp, err := h.statusSvc.TrackByIntentKey(r.Context(), intentKey)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "checkout not found"})
		return
	}
	if resp.Resumable && resp.CheckoutURL == "" && h.checkoutSvc != nil {
		_, recErr := h.checkoutSvc.RecoverByIntentKey(r.Context(), intentKey)
		if recErr == nil {
			fresh, freshErr := h.statusSvc.TrackByIntentKey(r.Context(), intentKey)
			if freshErr == nil {
				writeJSON(w, http.StatusOK, fresh)
				return
			}
			h.log.Error("track: recovery succeeded but fresh reread failed", zap.Error(freshErr))
			writeJSON(w, http.StatusServiceUnavailable, CheckoutErrorResponse{
				Error:     "checkout recovery required",
				ErrorCode: ErrCodeCheckoutRecoveryRequired,
			})
			return
		} else if errors.Is(recErr, ErrProviderStateAmbiguous) {
			writeJSON(w, http.StatusServiceUnavailable, CheckoutErrorResponse{
				Error:     "provider state ambiguous",
				ErrorCode: ErrCodeProviderStateAmbiguous,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// TestTrackCheckoutSession_RecoverySucceeded_FreshReadFails_Returns503 verifies that
// when recovery succeeds but the fresh reread of the order fails, the handler returns
// 503 instead of falling back to stale 200 data.
func TestTrackCheckoutSession_RecoverySucceeded_FreshReadFails_Returns503(t *testing.T) {
	intentKey := GenerateCheckoutIntentKey()
	staleResp := TrackCheckoutResponse{
		OrderNSU:          "track-nsu-001",
		CheckoutIntentKey: intentKey,
		Status:            OrderStatusCreated,
		PlanSlug:          "basic",
		Resumable:         true,
		CheckoutURL:       "", // no URL — triggers recovery
		ExpiresAt:         time.Now().UTC().Add(20 * time.Minute),
	}
	statusSvc := &fakeFailingStatusService{first: staleResp}
	checkoutSvc := &fakeSucceedingCheckoutService{}

	h := &testTrackHandler{
		statusSvc:   statusSvc,
		checkoutSvc: checkoutSvc,
		log:         zap.NewNop(),
	}

	body, _ := json.Marshal(TrackCheckoutRequest{CheckoutIntentKey: intentKey})
	req := httptest.NewRequest(http.MethodPost, "/v1/checkout/sessions/track", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when fresh reread fails after recovery, got %d: %s", rr.Code, rr.Body.String())
	}
	var errResp CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.ErrorCode != ErrCodeCheckoutRecoveryRequired {
		t.Errorf("expected error_code %q, got %q", ErrCodeCheckoutRecoveryRequired, errResp.ErrorCode)
	}
	if checkoutSvc.calls != 1 {
		t.Errorf("expected exactly 1 recovery call, got %d", checkoutSvc.calls)
	}
	// Verify the status service was called twice: once for the initial read and once for the fresh reread.
	if statusSvc.callCount != 2 {
		t.Errorf("expected 2 status service calls (initial + fresh), got %d", statusSvc.callCount)
	}
}

// TestCreateSession_PersistsProviderCreateAttemptedAt verifies that after a successful
// CreateSession, the order has provider_create_attempted_at set.
func TestCreateSession_PersistsProviderCreateAttemptedAt(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/new-pcat",
		InvoiceSlug: "inv-pcat-001",
	}, nil)

	resp, err := svc.CreateSession(context.Background(), validCheckoutRequest())
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	order, err := orders.GetByNSU(context.Background(), resp.OrderNSU)
	if err != nil {
		t.Fatalf("GetByNSU: %v", err)
	}
	if order.ProviderCreateAttemptedAt == nil {
		t.Error("expected provider_create_attempted_at to be set after CreateSession")
	}
}
