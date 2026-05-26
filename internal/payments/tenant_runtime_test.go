package payments

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/organizations"
	commercialplans "github.com/villenneve/vil-core/internal/plans"
	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/tenants"
)

func TestCheckoutService_CreateSession_UsesVersionedPlanForTenantScope(t *testing.T) {
	orderRepo := newFakeOrderRepo()
	provider := &fakeProvider{resp: InfinitePayCheckoutResponse{CheckoutURL: "https://checkout.example/versioned", InvoiceSlug: "inv-versioned"}}
	service := NewCheckoutService(
		&fakeVersionedPlanRepo{plan: &commercialplans.Plan{
			ID:              primitive.NewObjectID(),
			PlanUUID:        "plan-v3",
			OrganizationID:  "org-001",
			TenantID:        "tenant-001",
			Slug:            "basic",
			Version:         3,
			Name:            "Plano Basic V3",
			BillingCycle:    commercialplans.BillingCycleMonthly,
			PriceCents:      12900,
			Currency:        "BRL",
			MaxInstallments: 3,
			Channel:         commercialplans.ChannelWeb,
			Active:          true,
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

	req := validCheckoutRequest()
	req.OrganizationSlug = "acme"
	req.TenantSlug = "clinic"

	resp, err := service.CreateSession(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	order, err := orderRepo.GetByNSU(context.Background(), resp.OrderNSU)
	if err != nil {
		t.Fatalf("GetByNSU returned error: %v", err)
	}
	if order.OrganizationID != "org-001" {
		t.Fatalf("expected organization_id org-001, got %q", order.OrganizationID)
	}
	if order.TenantID != "tenant-001" {
		t.Fatalf("expected tenant_id tenant-001, got %q", order.TenantID)
	}
	if order.PlanID != "plan-v3" {
		t.Fatalf("expected plan_id plan-v3, got %q", order.PlanID)
	}
	if order.PlanVersion != 3 {
		t.Fatalf("expected plan_version 3, got %d", order.PlanVersion)
	}
	if order.PlanSource != versionedPlanSource {
		t.Fatalf("expected plan_source %q, got %q", versionedPlanSource, order.PlanSource)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider to be called once, got %d", provider.calls)
	}
}

func TestPlanQueryService_ListActivePlans_UsesTenantVersionedCatalog(t *testing.T) {
	service := NewPlanQueryService(
		&fakeVersionedPlanRepo{plan: &commercialplans.Plan{
			ID:              primitive.NewObjectID(),
			PlanUUID:        "plan-v4",
			OrganizationID:  "org-001",
			TenantID:        "tenant-001",
			Slug:            "pro",
			Version:         4,
			Name:            "Plano Pro",
			BillingCycle:    commercialplans.BillingCycleAnnual,
			PriceCents:      29900,
			Currency:        "BRL",
			MaxInstallments: 12,
			Channel:         commercialplans.ChannelAll,
			Active:          true,
			ValidFrom:       time.Now().UTC().Add(-time.Hour),
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
		}},
		&fakeOrganizationRepo{org: &organizations.Organization{OrgUUID: "org-001", Slug: "acme", Active: true}},
		&fakeTenantRepo{tenant: &tenants.Tenant{TenantUUID: "tenant-001", OrganizationID: "org-001", Slug: "clinic", Active: true}},
		zap.NewNop(),
	)

	plans, err := service.ListActivePlans(context.Background(), PlanListQuery{
		OrganizationSlug: "acme",
		TenantSlug:       "clinic",
		Channel:          "web",
	})
	if err != nil {
		t.Fatalf("ListActivePlans returned error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].Slug != "pro" {
		t.Fatalf("expected slug pro, got %q", plans[0].Slug)
	}
	if plans[0].BillingCycle != string(commercialplans.BillingCycleAnnual) {
		t.Fatalf("expected annual billing cycle, got %q", plans[0].BillingCycle)
	}
	if plans[0].PriceCents != 29900 {
		t.Fatalf("expected price_cents 29900, got %d", plans[0].PriceCents)
	}
	if plans[0].ID != "plan-v4" {
		t.Fatalf("expected id plan-v4, got %q", plans[0].ID)
	}
	if !plans[0].Active {
		t.Fatal("expected active true")
	}
	if plans[0].MaxInstallments != 12 {
		t.Fatalf("expected max_installments 12, got %d", plans[0].MaxInstallments)
	}
}

func TestPlanQueryService_ListActivePlans_SameSlugBothCycles(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeMultiVersionedPlanRepo{
		plans: []commercialplans.Plan{
			{
				PlanUUID:        "plan-monthly-1",
				OrganizationID:  "org-001",
				TenantID:        "tenant-001",
				Slug:            "pro",
				Version:         1,
				Name:            "Plano Pro Mensal",
				BillingCycle:    commercialplans.BillingCycleMonthly,
				PriceCents:      9900,
				Currency:        "BRL",
				MaxInstallments: 1,
				Channel:         commercialplans.ChannelAll,
				Active:          true,
				ValidFrom:       now.Add(-time.Hour),
				CreatedAt:       now,
				UpdatedAt:       now,
			},
			{
				PlanUUID:        "plan-annual-1",
				OrganizationID:  "org-001",
				TenantID:        "tenant-001",
				Slug:            "pro",
				Version:         1,
				Name:            "Plano Pro Anual",
				BillingCycle:    commercialplans.BillingCycleAnnual,
				PriceCents:      99000,
				Currency:        "BRL",
				MaxInstallments: 12,
				Channel:         commercialplans.ChannelAll,
				Active:          true,
				ValidFrom:       now.Add(-time.Hour),
				CreatedAt:       now,
				UpdatedAt:       now,
			},
		},
	}
	service := NewPlanQueryService(
		repo,
		&fakeOrganizationRepo{org: &organizations.Organization{OrgUUID: "org-001", Slug: "acme", Active: true}},
		&fakeTenantRepo{tenant: &tenants.Tenant{TenantUUID: "tenant-001", OrganizationID: "org-001", Slug: "clinic", Active: true}},
		zap.NewNop(),
	)

	plans, err := service.ListActivePlans(context.Background(), PlanListQuery{
		OrganizationSlug: "acme",
		TenantSlug:       "clinic",
		Channel:          "web",
	})
	if err != nil {
		t.Fatalf("ListActivePlans returned error: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans (monthly + annual for same slug), got %d", len(plans))
	}

	byID := make(map[string]PlanResponse, 2)
	for _, p := range plans {
		byID[p.ID] = p
	}

	monthly, ok := byID["plan-monthly-1"]
	if !ok {
		t.Fatal("expected plan-monthly-1 in response")
	}
	if monthly.BillingCycle != commercialplans.BillingCycleMonthly {
		t.Errorf("expected monthly billing cycle, got %q", monthly.BillingCycle)
	}
	if monthly.MaxInstallments != 1 {
		t.Errorf("expected max_installments 1, got %d", monthly.MaxInstallments)
	}

	annual, ok := byID["plan-annual-1"]
	if !ok {
		t.Fatal("expected plan-annual-1 in response")
	}
	if annual.BillingCycle != commercialplans.BillingCycleAnnual {
		t.Errorf("expected annual billing cycle, got %q", annual.BillingCycle)
	}
	if annual.MaxInstallments != 12 {
		t.Errorf("expected max_installments 12, got %d", annual.MaxInstallments)
	}
}
