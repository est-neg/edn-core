package tenants_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/villenneve/vil-core/internal/tenants"
)

// ─── Fake repository ─────────────────────────────────────────────────────────

type fakeRepo struct {
	byUUID    map[string]*tenants.Tenant
	byOrgSlug map[string]*tenants.Tenant // key: "orgID:slug"
}

func newFake() *fakeRepo {
	return &fakeRepo{
		byUUID:    make(map[string]*tenants.Tenant),
		byOrgSlug: make(map[string]*tenants.Tenant),
	}
}

func orgSlugKey(orgID, slug string) string { return orgID + ":" + slug }

func (r *fakeRepo) Create(_ context.Context, t tenants.Tenant) error {
	if _, exists := r.byUUID[t.TenantUUID]; exists {
		return tenants.ErrDuplicate
	}
	k := orgSlugKey(t.OrganizationID, t.Slug)
	if _, exists := r.byOrgSlug[k]; exists {
		return tenants.ErrDuplicate
	}
	r.byUUID[t.TenantUUID] = &t
	r.byOrgSlug[k] = &t
	return nil
}

func (r *fakeRepo) FindByUUID(_ context.Context, uuid string) (*tenants.Tenant, error) {
	t, ok := r.byUUID[uuid]
	if !ok {
		return nil, tenants.ErrNotFound
	}
	return t, nil
}

func (r *fakeRepo) FindByOrgAndSlug(_ context.Context, orgID, slug string) (*tenants.Tenant, error) {
	t, ok := r.byOrgSlug[orgSlugKey(orgID, slug)]
	if !ok {
		return nil, tenants.ErrNotFound
	}
	return t, nil
}

func (r *fakeRepo) ListByOrg(_ context.Context, orgID string) ([]tenants.Tenant, error) {
	var out []tenants.Tenant
	for _, t := range r.byUUID {
		if t.OrganizationID == orgID {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (r *fakeRepo) Update(_ context.Context, tenantUUID string, tenant tenants.Tenant) error {
	t, ok := r.byUUID[tenantUUID]
	if !ok {
		return tenants.ErrNotFound
	}
	t.Name = tenant.Name
	t.Channels = tenant.Channels
	t.Active = tenant.Active
	return nil
}

func (r *fakeRepo) Deactivate(_ context.Context, tenantUUID string) error {
	t, ok := r.byUUID[tenantUUID]
	if !ok {
		return tenants.ErrNotFound
	}
	t.Active = false
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newTenant(uuid, orgID, slug string) tenants.Tenant {
	return tenants.Tenant{
		ID:             primitive.NewObjectID(),
		TenantUUID:     uuid,
		OrganizationID: orgID,
		Slug:           slug,
		Name:           "Test Tenant",
		Channels:       []string{"web"},
		Active:         true,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestTenant_Create_Success(t *testing.T) {
	repo := newFake()
	tenant := newTenant("t-001", "org-001", "main")
	if err := repo.Create(context.Background(), tenant); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := repo.FindByOrgAndSlug(context.Background(), "org-001", "main")
	if err != nil {
		t.Fatalf("FindByOrgAndSlug failed: %v", err)
	}
	if got.TenantUUID != "t-001" {
		t.Errorf("expected t-001, got %q", got.TenantUUID)
	}
}

// Same slug in different organizations must be allowed.
func TestTenant_SameSlug_DifferentOrgs_Allowed(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newTenant("t-001", "org-001", "main"))

	err := repo.Create(context.Background(), newTenant("t-002", "org-002", "main"))
	if err != nil {
		t.Fatalf("same slug in different orgs should succeed, got: %v", err)
	}
}

// Same slug in the same organization must be rejected.
func TestTenant_DuplicateSlug_SameOrg_Rejected(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newTenant("t-001", "org-001", "main"))

	err := repo.Create(context.Background(), newTenant("t-002", "org-001", "main"))
	if !errors.Is(err, tenants.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for same org+slug, got %v", err)
	}
}

func TestTenant_FindByOrgAndSlug_NotFound(t *testing.T) {
	repo := newFake()
	_, err := repo.FindByOrgAndSlug(context.Background(), "org-999", "missing")
	if !errors.Is(err, tenants.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestTenant_ListByOrg_ReturnsOnlyOwned(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newTenant("t-001", "org-001", "tenant-a"))
	_ = repo.Create(context.Background(), newTenant("t-002", "org-001", "tenant-b"))
	_ = repo.Create(context.Background(), newTenant("t-003", "org-002", "tenant-c"))

	list, err := repo.ListByOrg(context.Background(), "org-001")
	if err != nil {
		t.Fatalf("ListByOrg error: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 tenants for org-001, got %d", len(list))
	}
}

// Resolving a tenant from an organization that does not own it must return ErrNotFound.
func TestTenant_ResolveCrossOrg_NotFound(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newTenant("t-001", "org-001", "main"))

	_, err := repo.FindByOrgAndSlug(context.Background(), "org-999", "main")
	if !errors.Is(err, tenants.ErrNotFound) {
		t.Fatalf("cross-org resolution must return ErrNotFound, got %v", err)
	}
}
