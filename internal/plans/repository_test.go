package plans_test

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/villenneve/vil-core/internal/plans"
)

// ─── In-memory fake ────────────────────────────────────────────────────────────

type fakeRepo struct {
	byUUID      map[string]*plans.Plan
	byTenantKey map[string]*plans.Plan // key: tenantID+"|"+slug+"|"+version
}

func newFake() *fakeRepo {
	return &fakeRepo{
		byUUID:      make(map[string]*plans.Plan),
		byTenantKey: make(map[string]*plans.Plan),
	}
}

func tenantKey(tenantID, slug string, version int) string {
	return tenantID + "|" + slug + "|" + string(rune('0'+version))
}

func (r *fakeRepo) Create(_ context.Context, p plans.Plan) error {
	if _, exists := r.byUUID[p.PlanUUID]; exists {
		return plans.ErrDuplicate
	}
	k := tenantKey(p.TenantID, p.Slug, p.Version)
	if _, exists := r.byTenantKey[k]; exists {
		return plans.ErrDuplicate
	}
	cp := p
	r.byUUID[p.PlanUUID] = &cp
	r.byTenantKey[k] = &cp
	return nil
}

func (r *fakeRepo) FindByUUID(_ context.Context, planUUID string) (*plans.Plan, error) {
	p, ok := r.byUUID[planUUID]
	if !ok {
		return nil, plans.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *fakeRepo) FindActiveByTenantSlug(_ context.Context, tenantID, slug string) (*plans.Plan, error) {
	var best *plans.Plan
	now := time.Now().UTC()
	for _, p := range r.byUUID {
		if p.TenantID == tenantID && p.Slug == slug && planActiveAt(p, now) {
			if best == nil || p.Version > best.Version {
				cp := *p
				best = &cp
			}
		}
	}
	if best == nil {
		return nil, plans.ErrNotFound
	}
	return best, nil
}

func (r *fakeRepo) FindSellableByTenantSlug(_ context.Context, tenantID, slug, billingCycle, channel string, at time.Time) (*plans.Plan, error) {
	var best *plans.Plan
	for _, p := range r.byUUID {
		if p.TenantID != tenantID || p.Slug != slug || p.BillingCycle != billingCycle || !planActiveAt(p, at) {
			continue
		}
		if channel != "" && channel != plans.ChannelAll && p.Channel != channel && p.Channel != plans.ChannelAll {
			continue
		}
		if best == nil || p.Version > best.Version {
			cp := *p
			best = &cp
		}
	}
	if best == nil {
		return nil, plans.ErrNotFound
	}
	return best, nil
}

func (r *fakeRepo) ListActiveByTenant(_ context.Context, tenantID, channel string) ([]plans.Plan, error) {
	now := time.Now().UTC()
	var out []plans.Plan
	for _, p := range r.byUUID {
		if p.TenantID != tenantID || !planActiveAt(p, now) {
			continue
		}
		if channel != "" && channel != plans.ChannelAll {
			if p.Channel != channel && p.Channel != plans.ChannelAll {
				continue
			}
		}
		out = append(out, *p)
	}
	return out, nil
}

func planActiveAt(plan *plans.Plan, at time.Time) bool {
	if !plan.Active || plan.ValidFrom.After(at) {
		return false
	}
	if plan.ValidUntil != nil && plan.ValidUntil.Before(at) {
		return false
	}
	return true
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newPlan(uuid, tenantID, slug string, version int, channel string, active bool) plans.Plan {
	now := time.Now().UTC()
	return plans.Plan{
		ID:              primitive.NewObjectID(),
		PlanUUID:        uuid,
		OrganizationID:  "org-001",
		TenantID:        tenantID,
		Slug:            slug,
		Version:         version,
		Name:            "Test Plan",
		PackageRef:      plans.PackageRef{PackageUUID: "pkg-001", Version: 1},
		PackageSnapshot: plans.PackageSnapshot{PackageUUID: "pkg-001", Name: "Base Package", Version: 1},
		BillingCycle:    plans.BillingCycleMonthly,
		PriceCents:      9900,
		Currency:        "BRL",
		MaxInstallments: 1,
		Channel:         channel,
		Active:          active,
		ValidFrom:       now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestCreate_Success(t *testing.T) {
	repo := newFake()
	p := newPlan("plan-001", "tenant-a", "basic", 1, plans.ChannelAll, true)

	if err := repo.Create(context.Background(), p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreate_DuplicateUUID(t *testing.T) {
	repo := newFake()
	p := newPlan("plan-dup", "tenant-a", "basic", 1, plans.ChannelAll, true)

	_ = repo.Create(context.Background(), p)
	err := repo.Create(context.Background(), p)
	if err != plans.ErrDuplicate {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestCreate_DuplicateTenantSlugVersion(t *testing.T) {
	repo := newFake()
	p1 := newPlan("plan-001", "tenant-a", "basic", 1, plans.ChannelAll, true)
	p2 := newPlan("plan-002", "tenant-a", "basic", 1, plans.ChannelAll, true)

	_ = repo.Create(context.Background(), p1)
	err := repo.Create(context.Background(), p2)
	if err != plans.ErrDuplicate {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestFindByUUID_Found(t *testing.T) {
	repo := newFake()
	p := newPlan("plan-find-1", "tenant-a", "pro", 1, plans.ChannelWeb, true)
	_ = repo.Create(context.Background(), p)

	got, err := repo.FindByUUID(context.Background(), "plan-find-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PlanUUID != "plan-find-1" {
		t.Errorf("got uuid %q, want %q", got.PlanUUID, "plan-find-1")
	}
}

func TestFindByUUID_NotFound(t *testing.T) {
	repo := newFake()
	_, err := repo.FindByUUID(context.Background(), "does-not-exist")
	if err != plans.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFindActiveByTenantSlug_ReturnsHighestVersion(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newPlan("plan-v1", "tenant-a", "starter", 1, plans.ChannelAll, true))
	_ = repo.Create(context.Background(), newPlan("plan-v2", "tenant-a", "starter", 2, plans.ChannelAll, true))

	got, err := repo.FindActiveByTenantSlug(context.Background(), "tenant-a", "starter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("expected version 2, got %d", got.Version)
	}
}

func TestFindActiveByTenantSlug_InactivePlanNotReturned(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newPlan("plan-inactive", "tenant-b", "basic", 1, plans.ChannelAll, false))

	_, err := repo.FindActiveByTenantSlug(context.Background(), "tenant-b", "basic")
	if err != plans.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFindSellableByTenantSlug_ReturnsHighestSellableVersion(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newPlan("plan-v1", "tenant-z", "starter", 1, plans.ChannelWeb, true))
	latest := newPlan("plan-v2", "tenant-z", "starter", 2, plans.ChannelAll, true)
	_ = repo.Create(context.Background(), latest)

	got, err := repo.FindSellableByTenantSlug(context.Background(), "tenant-z", "starter", plans.BillingCycleMonthly, plans.ChannelWeb, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("expected version 2, got %d", got.Version)
	}
}

func TestFindSellableByTenantSlug_RespectsValidityWindow(t *testing.T) {
	repo := newFake()
	future := newPlan("plan-future", "tenant-z", "future", 1, plans.ChannelAll, true)
	future.ValidFrom = time.Now().UTC().Add(2 * time.Hour)
	_ = repo.Create(context.Background(), future)

	_, err := repo.FindSellableByTenantSlug(context.Background(), "tenant-z", "future", plans.BillingCycleMonthly, plans.ChannelWeb, time.Now().UTC())
	if err != plans.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListActiveByTenant_NoChannelFilter(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newPlan("p1", "tenant-c", "a", 1, plans.ChannelWeb, true))
	_ = repo.Create(context.Background(), newPlan("p2", "tenant-c", "b", 1, plans.ChannelMobile, true))
	_ = repo.Create(context.Background(), newPlan("p3", "tenant-c", "c", 1, plans.ChannelAll, false)) // inactive

	got, err := repo.ListActiveByTenant(context.Background(), "tenant-c", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 active plans, got %d", len(got))
	}
}

func TestListActiveByTenant_ChannelFilter(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newPlan("p-web", "tenant-d", "a", 1, plans.ChannelWeb, true))
	_ = repo.Create(context.Background(), newPlan("p-mob", "tenant-d", "b", 1, plans.ChannelMobile, true))
	_ = repo.Create(context.Background(), newPlan("p-all", "tenant-d", "c", 1, plans.ChannelAll, true))

	got, err := repo.ListActiveByTenant(context.Background(), "tenant-d", plans.ChannelWeb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return web-scoped + all-scoped plans.
	if len(got) != 2 {
		t.Errorf("expected 2 plans (web + all), got %d", len(got))
	}
	for _, p := range got {
		if p.Channel != plans.ChannelWeb && p.Channel != plans.ChannelAll {
			t.Errorf("unexpected channel %q in result", p.Channel)
		}
	}
}

func TestListActiveByTenant_OtherTenantIsolated(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newPlan("p-t1", "tenant-e", "x", 1, plans.ChannelAll, true))
	_ = repo.Create(context.Background(), newPlan("p-t2", "tenant-f", "x", 1, plans.ChannelAll, true))

	got, err := repo.ListActiveByTenant(context.Background(), "tenant-e", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 plan for tenant-e, got %d", len(got))
	}
	if got[0].TenantID != "tenant-e" {
		t.Errorf("got tenantID %q, want tenant-e", got[0].TenantID)
	}
}
