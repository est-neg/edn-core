package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/idempotency"
	"github.com/villenneve/vil-core/internal/organizations"
	commercialplans "github.com/villenneve/vil-core/internal/plans"
	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/tenants"
)

type fakeVersionedPlanRepo struct {
	plan *commercialplans.Plan
}

func (r *fakeVersionedPlanRepo) FindSellableByTenantSlug(_ context.Context, tenantID, slug, billingCycle, channel string, _ time.Time) (*commercialplans.Plan, error) {
	if r.plan == nil || r.plan.TenantID != tenantID || r.plan.Slug != slug || r.plan.BillingCycle != billingCycle {
		return nil, ErrPlanNotFound
	}
	if channel != "" && channel != commercialplans.ChannelAll && r.plan.Channel != channel && r.plan.Channel != commercialplans.ChannelAll {
		return nil, ErrPlanNotFound
	}
	return r.plan, nil
}

func (r *fakeVersionedPlanRepo) ListActiveByTenant(_ context.Context, tenantID, channel string) ([]commercialplans.Plan, error) {
	if r.plan == nil || r.plan.TenantID != tenantID {
		return nil, nil
	}
	if channel != "" && channel != commercialplans.ChannelAll && r.plan.Channel != channel && r.plan.Channel != commercialplans.ChannelAll {
		return nil, nil
	}
	return []commercialplans.Plan{*r.plan}, nil
}

func (r *fakeVersionedPlanRepo) FindByUUID(_ context.Context, planUUID string) (*commercialplans.Plan, error) {
	if r.plan == nil || r.plan.PlanUUID != planUUID {
		return nil, ErrPlanNotFound
	}
	return r.plan, nil
}

// fakeMultiVersionedPlanRepo is a fake for tests that need multiple plans returned by ListActiveByTenant.
type fakeMultiVersionedPlanRepo struct {
	plans []commercialplans.Plan
}

func (r *fakeMultiVersionedPlanRepo) FindSellableByTenantSlug(_ context.Context, tenantID, slug, billingCycle, channel string, _ time.Time) (*commercialplans.Plan, error) {
	for i := range r.plans {
		p := &r.plans[i]
		if p.TenantID != tenantID || p.Slug != slug || p.BillingCycle != billingCycle {
			continue
		}
		if channel != "" && channel != commercialplans.ChannelAll && p.Channel != channel && p.Channel != commercialplans.ChannelAll {
			continue
		}
		return p, nil
	}
	return nil, ErrPlanNotFound
}

func (r *fakeMultiVersionedPlanRepo) ListActiveByTenant(_ context.Context, tenantID, channel string) ([]commercialplans.Plan, error) {
	var out []commercialplans.Plan
	for _, p := range r.plans {
		if p.TenantID != tenantID {
			continue
		}
		if channel != "" && channel != commercialplans.ChannelAll && p.Channel != channel && p.Channel != commercialplans.ChannelAll {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *fakeMultiVersionedPlanRepo) FindByUUID(_ context.Context, planUUID string) (*commercialplans.Plan, error) {
	for i := range r.plans {
		if r.plans[i].PlanUUID == planUUID {
			return &r.plans[i], nil
		}
	}
	return nil, ErrPlanNotFound
}

type fakeOrganizationRepo struct {
	org *organizations.Organization
}

func (r *fakeOrganizationRepo) FindBySlug(_ context.Context, slug string) (*organizations.Organization, error) {
	if r.org == nil || r.org.Slug != slug {
		return nil, ErrOrganizationNotFound
	}
	return r.org, nil
}

type fakeTenantRepo struct {
	tenant *tenants.Tenant
}

func (r *fakeTenantRepo) FindByOrgAndSlug(_ context.Context, organizationID, slug string) (*tenants.Tenant, error) {
	if r.tenant == nil || r.tenant.OrganizationID != organizationID || r.tenant.Slug != slug {
		return nil, ErrTenantNotFound
	}
	return r.tenant, nil
}

type fakeOrderRepo struct {
	orders                         map[string]*checkout.Order
	updateProviderURLErr           error
	updateStatusErr                error
	markProviderCreateAttemptedErr error
}

func newFakeOrderRepo() *fakeOrderRepo {
	return &fakeOrderRepo{orders: make(map[string]*checkout.Order)}
}

func (r *fakeOrderRepo) Create(_ context.Context, order checkout.Order) error {
	copy := order
	r.orders[order.OrderNSU] = &copy
	return nil
}

func (r *fakeOrderRepo) GetByNSU(_ context.Context, orderNSU string) (*checkout.Order, error) {
	order, ok := r.orders[orderNSU]
	if !ok {
		return nil, ErrOrderNotFound
	}
	copy := *order
	return &copy, nil
}

func (r *fakeOrderRepo) FindByIntentKey(_ context.Context, intentKey string) (*checkout.Order, error) {
	for _, order := range r.orders {
		if order.CheckoutIntentKey == intentKey {
			copy := *order
			return &copy, nil
		}
	}
	return nil, ErrOrderNotFound
}

func (r *fakeOrderRepo) UpdateStatus(_ context.Context, orderNSU string, status OrderStatus, updatedAt time.Time) error {
	if r.updateStatusErr != nil {
		return r.updateStatusErr
	}
	order, ok := r.orders[orderNSU]
	if !ok {
		return ErrOrderNotFound
	}
	order.Status = string(status)
	order.UpdatedAt = updatedAt
	return nil
}

func (r *fakeOrderRepo) UpdateProviderURL(_ context.Context, orderNSU, checkoutURL, invoiceSlug string, updatedAt time.Time) error {
	if r.updateProviderURLErr != nil {
		return r.updateProviderURLErr
	}
	order, ok := r.orders[orderNSU]
	if !ok {
		return ErrOrderNotFound
	}
	order.ProviderCheckoutURL = checkoutURL
	order.InvoiceSlug = invoiceSlug
	order.UpdatedAt = updatedAt
	return nil
}

func (r *fakeOrderRepo) UpdateReceipt(_ context.Context, orderNSU, receiptURL string, updatedAt time.Time) error {
	order, ok := r.orders[orderNSU]
	if !ok {
		return ErrOrderNotFound
	}
	order.ReceiptURL = receiptURL
	order.UpdatedAt = updatedAt
	return nil
}

func (r *fakeOrderRepo) FindByCustomerDocument(_ context.Context, normalizedDocument string) ([]checkout.Order, error) {
	var out []checkout.Order
	for _, o := range r.orders {
		if o.CustomerDocument == normalizedDocument {
			out = append(out, *o)
		}
	}
	if out == nil {
		out = []checkout.Order{}
	}
	return out, nil
}

func (r *fakeOrderRepo) MarkProviderCreateAttempted(_ context.Context, orderNSU string, attemptedAt time.Time) error {
	if r.markProviderCreateAttemptedErr != nil {
		return r.markProviderCreateAttemptedErr
	}
	order, ok := r.orders[orderNSU]
	if !ok {
		return ErrOrderNotFound
	}
	t := attemptedAt
	order.ProviderCreateAttemptedAt = &t
	order.UpdatedAt = attemptedAt
	return nil
}

type fakeProvider struct {
	calls int
	resp  InfinitePayCheckoutResponse
	err   error
}

func (p *fakeProvider) CreateCheckout(_ context.Context, _ InfinitePayCheckoutRequest) (InfinitePayCheckoutResponse, error) {
	p.calls++
	if p.err != nil {
		return InfinitePayCheckoutResponse{}, p.err
	}
	return p.resp, nil
}

func (p *fakeProvider) VerifyPayment(_ context.Context, _, _, _ string) (InfinitePayVerifyResponse, error) {
	return InfinitePayVerifyResponse{}, nil
}

type fakeLockManager struct{}

func (fakeLockManager) AcquireOrderLock(context.Context, string, string) error { return nil }
func (fakeLockManager) ReleaseOrderLock(context.Context, string, string) error { return nil }

type fakeStatusCache struct{}

func (fakeStatusCache) GetOrderStatus(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (fakeStatusCache) SetOrderStatus(context.Context, string, string) error { return nil }
func (fakeStatusCache) InvalidateOrderStatus(context.Context, string) error  { return nil }

type fakeCheckoutIdempotencyRepo struct {
	records   map[string]*idempotency.Key
	commitErr error
}

func newFakeCheckoutIdempotencyRepo() *fakeCheckoutIdempotencyRepo {
	return &fakeCheckoutIdempotencyRepo{records: make(map[string]*idempotency.Key)}
}

func idemLookup(tenantID, operation, key string) string {
	return tenantID + ":" + operation + ":" + key
}

func (r *fakeCheckoutIdempotencyRepo) Reserve(_ context.Context, key idempotency.Key) error {
	lookup := idemLookup(key.TenantID, key.Operation, key.IdempotencyKey)
	if _, exists := r.records[lookup]; exists {
		return idempotency.ErrDuplicate
	}
	copy := key
	r.records[lookup] = &copy
	return nil
}

func (r *fakeCheckoutIdempotencyRepo) FindByTenantOpKey(_ context.Context, tenantID, operation, idempotencyKey string) (*idempotency.Key, error) {
	record, ok := r.records[idemLookup(tenantID, operation, idempotencyKey)]
	if !ok {
		return nil, idempotency.ErrNotFound
	}
	copy := *record
	return &copy, nil
}

func (r *fakeCheckoutIdempotencyRepo) FindByTenantOpResourceID(_ context.Context, tenantID, operation, resourceID string) (*idempotency.Key, error) {
	for _, record := range r.records {
		if record.TenantID == tenantID && record.Operation == operation && record.ResourceID == resourceID {
			copy := *record
			return &copy, nil
		}
	}
	return nil, idempotency.ErrNotFound
}

func (r *fakeCheckoutIdempotencyRepo) Commit(_ context.Context, tenantID, operation, idempotencyKey, resourceID, resourceStatus, resourceURL, externalRef string, updatedAt time.Time) error {
	if r.commitErr != nil {
		return r.commitErr
	}
	record, ok := r.records[idemLookup(tenantID, operation, idempotencyKey)]
	if !ok {
		return idempotency.ErrNotFound
	}
	record.ResourceID = resourceID
	record.ResourceStatus = resourceStatus
	record.ResourceURL = resourceURL
	record.ExternalRef = externalRef
	record.Status = idempotency.StatusCommitted
	record.UpdatedAt = updatedAt
	return nil
}

func (r *fakeCheckoutIdempotencyRepo) Fail(_ context.Context, tenantID, operation, idempotencyKey string, updatedAt time.Time) error {
	record, ok := r.records[idemLookup(tenantID, operation, idempotencyKey)]
	if !ok {
		return idempotency.ErrNotFound
	}
	record.Status = idempotency.StatusFailed
	record.UpdatedAt = updatedAt
	return nil
}

func newCheckoutServiceForIdempotencyTests() (*CheckoutService, *fakeProvider) {
	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/session", InvoiceSlug: "inv-123"}}
	service := NewCheckoutService(
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
			MaxInstallments: 1,
			Channel:         commercialplans.ChannelAll,
			Active:          true,
			ValidFrom:       time.Now().UTC().Add(-time.Hour),
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
		}},
		&fakeOrganizationRepo{org: &organizations.Organization{OrgUUID: "org-001", Slug: "acme", Active: true}},
		&fakeTenantRepo{tenant: &tenants.Tenant{TenantUUID: "tenant-001", OrganizationID: "org-001", Slug: "clinic", Active: true}},
		newFakeOrderRepo(),
		newFakeCheckoutIdempotencyRepo(),
		provider,
		fakeLockManager{},
		fakeStatusCache{},
		config.PaymentsConfig{},
		zap.NewNop(),
	)
	return service, provider
}

func validCheckoutRequest() CreateCheckoutRequest {
	return CreateCheckoutRequest{
		OrganizationSlug: "acme",
		TenantSlug:       "clinic",
		Channel:          "web",
		PlanSlug:         "basic",
		BillingCycle:     "monthly",
		IdempotencyKey:   "checkout-key-001",
		Customer: CustomerPayload{
			Name:     "Joao Silva",
			Email:    "joao@example.com",
			Phone:    "11987654321",
			Document: "529.982.247-25",
		},
	}
}

func TestCheckoutService_CreateSession_ReusesSameKeyAndPayload(t *testing.T) {
	service, provider := newCheckoutServiceForIdempotencyTests()
	request := validCheckoutRequest()

	first, err := service.CreateSession(context.Background(), request)
	if err != nil {
		t.Fatalf("first CreateSession returned error: %v", err)
	}

	second, err := service.CreateSession(context.Background(), request)
	if err != nil {
		t.Fatalf("second CreateSession returned error: %v", err)
	}

	if first.OrderNSU != second.OrderNSU {
		t.Fatalf("expected same order NSU, got %q and %q", first.OrderNSU, second.OrderNSU)
	}
	if first.CheckoutURL != second.CheckoutURL {
		t.Fatalf("expected same checkout URL, got %q and %q", first.CheckoutURL, second.CheckoutURL)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider to be called once, got %d", provider.calls)
	}
	if second.Status != OrderStatusCheckoutCreated {
		t.Fatalf("expected checkout_created on replay, got %q", second.Status)
	}
}

func TestCheckoutService_CreateSession_RejectsSameKeyDifferentPayload(t *testing.T) {
	service, provider := newCheckoutServiceForIdempotencyTests()
	first := validCheckoutRequest()
	if _, err := service.CreateSession(context.Background(), first); err != nil {
		t.Fatalf("first CreateSession returned error: %v", err)
	}

	second := validCheckoutRequest()
	second.Customer.Name = "Maria Souza"

	_, err := service.CreateSession(context.Background(), second)
	if !errors.Is(err, ErrCheckoutConflict) {
		t.Fatalf("expected ErrCheckoutConflict, got %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider to be called once, got %d", provider.calls)
	}
}

func TestCheckoutService_CreateSession_MissingIdempotencyKeyIsRejected(t *testing.T) {
	service, provider := newCheckoutServiceForIdempotencyTests()
	request := validCheckoutRequest()
	request.IdempotencyKey = ""

	_, err := service.CreateSession(context.Background(), request)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
	if provider.calls != 0 {
		t.Fatalf("expected provider to not be called, got %d", provider.calls)
	}
}

func TestCheckoutService_CreateSession_PersistFailAfterCommit_ReturnsRecoveryRequiredThenReplaySucceeds(t *testing.T) {
	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/recover", InvoiceSlug: "inv-recover"}}
	orders := newFakeOrderRepo()
	orders.updateProviderURLErr = errors.New("write concern timeout")
	orders.updateStatusErr = errors.New("write concern timeout")
	idempotencyRepo := newFakeCheckoutIdempotencyRepo()
	service := NewCheckoutService(
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
		orders,
		idempotencyRepo,
		provider,
		fakeLockManager{},
		fakeStatusCache{},
		config.PaymentsConfig{},
		zap.NewNop(),
	)

	req := validCheckoutRequest()
	// First call: idempotency commit succeeded but order persist failed → recovery required.
	_, err := service.CreateSession(context.Background(), req)
	if !errors.Is(err, ErrCheckoutRecoveryRequired) {
		t.Fatalf("expected ErrCheckoutRecoveryRequired, got %v", err)
	}
	// Same-key retry: replays from idempotency snapshot without calling the provider again.
	second, err := service.CreateSession(context.Background(), req)
	if err != nil {
		t.Fatalf("replay CreateSession returned error: %v", err)
	}
	if second.CheckoutURL != "https://checkout.example/recover" {
		t.Fatalf("expected checkout URL from idempotency snapshot, got %q", second.CheckoutURL)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider to be called once, got %d", provider.calls)
	}
}

func TestCheckoutService_CreateSession_BothCommitAndPersistFail_ReturnsProviderStateAmbiguous(t *testing.T) {
	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/ambiguous", InvoiceSlug: "inv-ambiguous"}}
	orders := newFakeOrderRepo()
	orders.updateProviderURLErr = errors.New("write concern timeout")
	orders.updateStatusErr = errors.New("write concern timeout")
	idempotencyRepo := newFakeCheckoutIdempotencyRepo()
	idempotencyRepo.commitErr = errors.New("mongo write timeout")
	service := NewCheckoutService(
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
		orders,
		idempotencyRepo,
		provider,
		fakeLockManager{},
		fakeStatusCache{},
		config.PaymentsConfig{},
		zap.NewNop(),
	)

	req := validCheckoutRequest()
	_, err := service.CreateSession(context.Background(), req)
	if !errors.Is(err, ErrProviderStateAmbiguous) {
		t.Fatalf("expected ErrProviderStateAmbiguous, got %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider to be called once, got %d", provider.calls)
	}
}
