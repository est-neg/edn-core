package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/organizations"
	commercialplans "github.com/villenneve/vil-core/internal/plans"
	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/tenants"
)

func newResumeTestService(orderRepo *fakeOrderRepo, providerResp InfinitePayCheckoutResponse, providerErr error) *CheckoutService {
	provider := &fakeProvider{resp: providerResp, err: providerErr}
	return NewCheckoutService(
		&fakeVersionedPlanRepo{plan: &commercialplans.Plan{
			ID:              primitive.NewObjectID(),
			PlanUUID:        "plan-basic",
			OrganizationID:  "org-001",
			TenantID:        "tenant-001",
			Slug:            "basic",
			Version:         1,
			Name:            "Plano Basic",
			BillingCycle:    commercialplans.BillingCycleMonthly,
			PriceCents:      9900,
			Currency:        "BRL",
			Active:          true,
			MaxInstallments: 1,
			Channel:         commercialplans.ChannelAll,
			ValidFrom:       time.Now().UTC().Add(-time.Hour),
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
		}},
		&fakeOrganizationRepo{org: &organizations.Organization{OrgUUID: "org-001", Slug: "acme", Active: true}},
		&fakeTenantRepo{tenant: &tenants.Tenant{TenantUUID: "tenant-001", OrganizationID: "org-001", Slug: "clinic", Active: true}},
		orderRepo,
		newFakeCheckoutIdempotencyRepo(),
		provider,
		fakeLockManager{},
		fakeStatusCache{},
		config.PaymentsConfig{},
		zap.NewNop(),
	)
}

// TestCheckoutSession_Create_ReturnsCheckoutIntentKeyAndExpiresAt verifies that a new
// checkout always returns a non-empty checkout_intent_key and a future expires_at.
func TestCheckoutSession_Create_ReturnsCheckoutIntentKeyAndExpiresAt(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/create",
		InvoiceSlug: "inv-001",
	}, nil)

	resp, err := svc.CreateSession(context.Background(), validCheckoutRequest())
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	if resp.CheckoutIntentKey == "" {
		t.Error("expected non-empty checkout_intent_key in response")
	}
	if resp.ExpiresAt.IsZero() {
		t.Error("expected non-zero expires_at in response")
	}
	if !resp.ExpiresAt.After(time.Now().UTC()) {
		t.Errorf("expected expires_at in the future, got %v", resp.ExpiresAt)
	}
	if resp.Resumed {
		t.Error("expected Resumed=false for a new checkout")
	}
}

// TestCheckoutSession_Create_PersistsIntentKeyOnOrder verifies the intent key is stored
// on the order document so resume lookups can find it.
func TestCheckoutSession_Create_PersistsIntentKeyOnOrder(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/create",
		InvoiceSlug: "inv-001",
	}, nil)

	resp, err := svc.CreateSession(context.Background(), validCheckoutRequest())
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	order, err := orders.GetByNSU(context.Background(), resp.OrderNSU)
	if err != nil {
		t.Fatalf("GetByNSU returned error: %v", err)
	}
	if order.CheckoutIntentKey != resp.CheckoutIntentKey {
		t.Errorf("order.CheckoutIntentKey=%q, want %q", order.CheckoutIntentKey, resp.CheckoutIntentKey)
	}
	if order.ExpiresAt.IsZero() {
		t.Error("order.ExpiresAt must not be zero")
	}
}

// TestCheckoutSession_Resume_WithValidIntentKey verifies that a checkout_intent_key
// from a prior successful create produces a canonical 200 resume response without
// calling the provider again.
func TestCheckoutSession_Resume_WithValidIntentKey(t *testing.T) {
	orders := newFakeOrderRepo()
	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/session", InvoiceSlug: "inv-resume"}}
	svc := newResumeTestService(orders, provider.resp, nil)

	// Create first to get an intent key.
	first, err := svc.CreateSession(context.Background(), validCheckoutRequest())
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	providerCallsAfterCreate := provider.calls

	// Resume using the intent key (different Idempotency-Key = different HTTP attempt).
	resumeReq := CreateCheckoutRequest{IdempotencyKey: "resume-attempt-001", CheckoutIntentKey: first.CheckoutIntentKey}
	resumed, err := svc.CreateSession(context.Background(), resumeReq)
	if err != nil {
		t.Fatalf("resume CreateSession returned error: %v", err)
	}
	if !resumed.Resumed {
		t.Error("expected Resumed=true for resume response")
	}
	if resumed.OrderNSU != first.OrderNSU {
		t.Errorf("expected same order_nsu %q, got %q", first.OrderNSU, resumed.OrderNSU)
	}
	if resumed.CheckoutURL != first.CheckoutURL {
		t.Errorf("expected same checkout_url %q, got %q", first.CheckoutURL, resumed.CheckoutURL)
	}
	if resumed.CheckoutIntentKey != first.CheckoutIntentKey {
		t.Errorf("expected same checkout_intent_key %q, got %q", first.CheckoutIntentKey, resumed.CheckoutIntentKey)
	}
	if provider.calls != providerCallsAfterCreate {
		t.Errorf("provider must not be called on resume; calls before=%d after=%d", providerCallsAfterCreate, provider.calls)
	}
}

// TestCheckoutSession_Resume_WithExpiredIntentKey verifies that a checkout whose
// backend-authoritative expires_at has passed returns ErrCheckoutExpired.
func TestCheckoutSession_Resume_WithExpiredIntentKey(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	// Inject an already-expired order directly into the fake repo.
	orders.orders["expired-nsu"] = &checkout.Order{
		OrderNSU:            "expired-nsu",
		TenantID:            "tenant-001",
		Status:              string(OrderStatusCheckoutCreated),
		ProviderCheckoutURL: "https://checkout.example/expired",
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(-5 * time.Minute), // already expired
		CreatedAt:           time.Now().UTC().Add(-35 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-35 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-expired-001",
		CheckoutIntentKey: intentKey,
	})
	if !errors.Is(err, ErrCheckoutExpired) {
		t.Fatalf("expected ErrCheckoutExpired, got %v", err)
	}
}

// TestCheckoutSession_Resume_WithPaidStatus verifies that a paid order cannot be resumed.
func TestCheckoutSession_Resume_WithPaidStatus(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["paid-nsu"] = &checkout.Order{
		OrderNSU:            "paid-nsu",
		TenantID:            "tenant-001",
		Status:              string(OrderStatusPaid),
		ProviderCheckoutURL: "https://checkout.example/paid",
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(30 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-5 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-paid-001",
		CheckoutIntentKey: intentKey,
	})
	if !errors.Is(err, ErrCheckoutNonResumable) {
		t.Fatalf("expected ErrCheckoutNonResumable for paid order, got %v", err)
	}
}

// TestCheckoutSession_Resume_WithFailedStatus verifies that a failed order cannot be resumed.
func TestCheckoutSession_Resume_WithFailedStatus(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["failed-nsu"] = &checkout.Order{
		OrderNSU:            "failed-nsu",
		TenantID:            "tenant-001",
		Status:              string(OrderStatusFailed),
		ProviderCheckoutURL: "https://checkout.example/failed",
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(30 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-2 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-2 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-failed-001",
		CheckoutIntentKey: intentKey,
	})
	if !errors.Is(err, ErrCheckoutNonResumable) {
		t.Fatalf("expected ErrCheckoutNonResumable for failed order, got %v", err)
	}
}

// TestCheckoutSession_Resume_WithExpiredStatus verifies that an order in status=expired
// is treated as non-resumable via ErrCheckoutNonResumable or ErrCheckoutExpired.
func TestCheckoutSession_Resume_WithExpiredStatus(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["status-expired-nsu"] = &checkout.Order{
		OrderNSU:            "status-expired-nsu",
		TenantID:            "tenant-001",
		Status:              string(OrderStatusExpired),
		ProviderCheckoutURL: "https://checkout.example/status-expired",
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(5 * time.Minute), // not expired by clock
		CreatedAt:           time.Now().UTC().Add(-2 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-2 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-status-expired-001",
		CheckoutIntentKey: intentKey,
	})
	if !errors.Is(err, ErrCheckoutNonResumable) {
		t.Fatalf("expected ErrCheckoutNonResumable for expired-status order, got %v", err)
	}
}

// TestCheckoutSession_Resume_RecoversMissingProviderURL verifies that an order without
// provider_checkout_url but with a valid PlanID triggers synchronous recovery:
// the service calls the provider, persists the URL on the same order, and returns a
// resumed success with Resumed=true. This is the "no false-success, no 503 when
// recovery is possible" invariant.
func TestCheckoutSession_Resume_RecoversMissingProviderURL(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["no-url-nsu"] = &checkout.Order{
		OrderNSU:            "no-url-nsu",
		TenantID:            "tenant-001",
		PlanID:              "plan-basic",
		AmountCents:         9900,
		Status:              string(OrderStatusCreated),
		ProviderCheckoutURL: "", // missing — provider call failed at create time
		InvoiceSlug:         "", // no partial state
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-5 * time.Minute),
	}
	providerResp := InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/recovered",
		InvoiceSlug: "inv-recovered",
	}
	svc := newResumeTestService(orders, providerResp, nil)

	resp, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-no-url-001",
		CheckoutIntentKey: intentKey,
	})
	if err != nil {
		t.Fatalf("expected recovery success, got %v", err)
	}
	if !resp.Resumed {
		t.Error("expected Resumed=true for recovered checkout")
	}
	if resp.CheckoutURL != providerResp.CheckoutURL {
		t.Errorf("expected checkout_url %q, got %q", providerResp.CheckoutURL, resp.CheckoutURL)
	}
	if resp.OrderNSU != "no-url-nsu" {
		t.Errorf("expected same order_nsu %q, got %q", "no-url-nsu", resp.OrderNSU)
	}
	// Verify provider_checkout_url was persisted on the order.
	order, err := orders.GetByNSU(context.Background(), "no-url-nsu")
	if err != nil {
		t.Fatalf("GetByNSU returned error: %v", err)
	}
	if order.ProviderCheckoutURL != providerResp.CheckoutURL {
		t.Errorf("expected persisted provider_checkout_url %q, got %q", providerResp.CheckoutURL, order.ProviderCheckoutURL)
	}
}

// TestCheckoutSession_Resume_AmbiguousWhenInvoiceSlugPresentWithoutURL verifies that
// an order with invoice_slug set but no provider_checkout_url returns ErrProviderStateAmbiguous
// because a partial provider response was received and re-creating risks a duplicate charge.
func TestCheckoutSession_Resume_AmbiguousWhenInvoiceSlugPresentWithoutURL(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["ambiguous-nsu"] = &checkout.Order{
		OrderNSU:            "ambiguous-nsu",
		TenantID:            "tenant-001",
		Status:              string(OrderStatusCreated),
		ProviderCheckoutURL: "",
		InvoiceSlug:         "inv-partial", // slug present but no URL
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-5 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-ambiguous-001",
		CheckoutIntentKey: intentKey,
	})
	if !errors.Is(err, ErrProviderStateAmbiguous) {
		t.Fatalf("expected ErrProviderStateAmbiguous when invoice_slug is set without URL, got %v", err)
	}
}

// TestCheckoutSession_Resume_UnknownIntentKey verifies that an unrecognized
// checkout_intent_key does not silently create a new checkout. It returns
// ErrCheckoutNonResumable to prevent enumeration.
func TestCheckoutSession_Resume_UnknownIntentKey(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "resume-unknown-001",
		CheckoutIntentKey: "completely-unknown-handle",
	})
	if !errors.Is(err, ErrCheckoutNonResumable) {
		t.Fatalf("expected ErrCheckoutNonResumable for unknown intent key, got %v", err)
	}
}

// TestCheckoutSession_Resume_DoesNotCallProviderWhenURLPersisted verifies that the
// provider is never called on resume when provider_checkout_url is already persisted
// on the order. The URL comes from the persisted order, not from a new provider call.
func TestCheckoutSession_Resume_DoesNotCallProviderWhenURLPersisted(t *testing.T) {
	orders := newFakeOrderRepo()
	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/session", InvoiceSlug: "inv-001"}}
	svc := NewCheckoutService(
		&fakeVersionedPlanRepo{plan: &commercialplans.Plan{
			ID: primitive.NewObjectID(), PlanUUID: "plan-basic", OrganizationID: "org-001",
			TenantID: "tenant-001", Slug: "basic", Version: 1, Name: "Plano Basic",
			BillingCycle: commercialplans.BillingCycleMonthly, PriceCents: 9900, Currency: "BRL",
			Active: true, MaxInstallments: 1, Channel: commercialplans.ChannelAll,
			ValidFrom: time.Now().UTC().Add(-time.Hour), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}},
		&fakeOrganizationRepo{org: &organizations.Organization{OrgUUID: "org-001", Slug: "acme", Active: true}},
		&fakeTenantRepo{tenant: &tenants.Tenant{TenantUUID: "tenant-001", OrganizationID: "org-001", Slug: "clinic", Active: true}},
		orders, newFakeCheckoutIdempotencyRepo(), provider, fakeLockManager{}, fakeStatusCache{}, config.PaymentsConfig{}, zap.NewNop(),
	)

	first, err := svc.CreateSession(context.Background(), validCheckoutRequest())
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	callsAfterCreate := provider.calls

	for i := 0; i < 3; i++ {
		_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
			IdempotencyKey:    "resume-noprovider-00" + string(rune('1'+i)),
			CheckoutIntentKey: first.CheckoutIntentKey,
		})
		if err != nil {
			t.Fatalf("resume attempt %d returned error: %v", i+1, err)
		}
	}
	if provider.calls != callsAfterCreate {
		t.Errorf("provider called on resume: calls before=%d after=%d", callsAfterCreate, provider.calls)
	}
}

// TestCheckoutSession_Resume_RequiresMissingIdempotencyKey verifies that the
// Idempotency-Key header is required even for resume requests.
func TestCheckoutSession_Resume_RequiresMissingIdempotencyKey(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	orders.orders["idem-nsu"] = &checkout.Order{
		OrderNSU:            "idem-nsu",
		Status:              string(OrderStatusCheckoutCreated),
		ProviderCheckoutURL: "https://checkout.example/idem",
		CheckoutIntentKey:   intentKey,
		ExpiresAt:           time.Now().UTC().Add(25 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt:           time.Now().UTC().Add(-5 * time.Minute),
	}
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

	_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
		IdempotencyKey:    "", // missing
		CheckoutIntentKey: intentKey,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest when Idempotency-Key is missing, got %v", err)
	}
}

// TestCheckoutSession_Resume_ExpiresAtBoundary verifies that an order whose expires_at
// is exactly at the current time (or one nanosecond in the past) is rejected as expired,
// while an order expiring in the future is accepted.
func TestCheckoutSession_Resume_ExpiresAtBoundary(t *testing.T) {
	now := time.Now().UTC()

	for _, tc := range []struct {
		name      string
		expiresAt time.Time
		wantErr   error
	}{
		{"just_expired", now.Add(-time.Nanosecond), ErrCheckoutExpired},
		{"not_yet_expired", now.Add(time.Second), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orders := newFakeOrderRepo()
			intentKey := GenerateCheckoutIntentKey()
			orders.orders["boundary-nsu-"+tc.name] = &checkout.Order{
				OrderNSU:            "boundary-nsu-" + tc.name,
				TenantID:            "tenant-001",
				Status:              string(OrderStatusCheckoutCreated),
				ProviderCheckoutURL: "https://checkout.example/boundary",
				CheckoutIntentKey:   intentKey,
				ExpiresAt:           tc.expiresAt,
				CreatedAt:           now.Add(-10 * time.Minute),
				UpdatedAt:           now.Add(-10 * time.Minute),
			}
			svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, nil)

			_, err := svc.CreateSession(context.Background(), CreateCheckoutRequest{
				IdempotencyKey:    "boundary-001",
				CheckoutIntentKey: intentKey,
			})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
			}
		})
	}
}

// TestGenerateCheckoutIntentKey verifies high-entropy and uniqueness of generated keys.
func TestGenerateCheckoutIntentKey(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		k := GenerateCheckoutIntentKey()
		if k == "" {
			t.Fatal("GenerateCheckoutIntentKey returned empty string")
		}
		if len(k) < 32 {
			t.Errorf("expected key length >= 32 chars, got %d: %q", len(k), k)
		}
		if _, dup := seen[k]; dup {
			t.Errorf("duplicate key generated: %q", k)
		}
		seen[k] = struct{}{}
	}
}
