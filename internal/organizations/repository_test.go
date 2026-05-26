package organizations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/villenneve/vil-core/internal/organizations"
)

// ─── Fake repository ─────────────────────────────────────────────────────────

type fakeRepo struct {
	byUUID map[string]*organizations.Organization
	bySlug map[string]*organizations.Organization
}

func newFake() *fakeRepo {
	return &fakeRepo{
		byUUID: make(map[string]*organizations.Organization),
		bySlug: make(map[string]*organizations.Organization),
	}
}

func (r *fakeRepo) Create(_ context.Context, org organizations.Organization) error {
	if _, exists := r.byUUID[org.OrgUUID]; exists {
		return organizations.ErrDuplicate
	}
	if _, exists := r.bySlug[org.Slug]; exists {
		return organizations.ErrDuplicate
	}
	r.byUUID[org.OrgUUID] = &org
	r.bySlug[org.Slug] = &org
	return nil
}

func (r *fakeRepo) FindByUUID(_ context.Context, orgUUID string) (*organizations.Organization, error) {
	o, ok := r.byUUID[orgUUID]
	if !ok {
		return nil, organizations.ErrNotFound
	}
	return o, nil
}

func (r *fakeRepo) FindBySlug(_ context.Context, slug string) (*organizations.Organization, error) {
	o, ok := r.bySlug[slug]
	if !ok {
		return nil, organizations.ErrNotFound
	}
	return o, nil
}

func (r *fakeRepo) List(_ context.Context) ([]organizations.Organization, error) {
	out := make([]organizations.Organization, 0, len(r.byUUID))
	for _, o := range r.byUUID {
		out = append(out, *o)
	}
	return out, nil
}

func (r *fakeRepo) Update(_ context.Context, orgUUID string, org organizations.Organization) error {
	o, ok := r.byUUID[orgUUID]
	if !ok {
		return organizations.ErrNotFound
	}
	o.Name = org.Name
	o.LegalName = org.LegalName
	o.CNPJ = org.CNPJ
	o.BillingEmail = org.BillingEmail
	o.Phone = org.Phone
	o.Address = org.Address
	o.Active = org.Active
	return nil
}

func (r *fakeRepo) Deactivate(_ context.Context, orgUUID string) error {
	o, ok := r.byUUID[orgUUID]
	if !ok {
		return organizations.ErrNotFound
	}
	o.Active = false
	return nil
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func newOrg(uuid, slug string) organizations.Organization {
	return organizations.Organization{
		ID:           primitive.NewObjectID(),
		OrgUUID:      uuid,
		Slug:         slug,
		Name:         "ACME Corp",
		BillingEmail: "billing@acme.example",
		Active:       true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

func TestOrganization_Create_Success(t *testing.T) {
	repo := newFake()
	org := newOrg("org-001", "acme")

	if err := repo.Create(context.Background(), org); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := repo.FindByUUID(context.Background(), "org-001")
	if err != nil {
		t.Fatalf("FindByUUID failed: %v", err)
	}
	if got.Slug != "acme" {
		t.Errorf("expected slug=acme, got %q", got.Slug)
	}
}

func TestOrganization_Create_DuplicateUUID(t *testing.T) {
	repo := newFake()
	org := newOrg("org-001", "acme")

	_ = repo.Create(context.Background(), org)

	duplicate := newOrg("org-001", "acme-other")
	err := repo.Create(context.Background(), duplicate)
	if !errors.Is(err, organizations.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestOrganization_Create_DuplicateSlug(t *testing.T) {
	repo := newFake()
	_ = repo.Create(context.Background(), newOrg("org-001", "acme"))

	err := repo.Create(context.Background(), newOrg("org-002", "acme"))
	if !errors.Is(err, organizations.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for slug collision, got %v", err)
	}
}

func TestOrganization_FindBySlug_NotFound(t *testing.T) {
	repo := newFake()
	_, err := repo.FindBySlug(context.Background(), "unknown")
	if !errors.Is(err, organizations.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestOrganization_FindByUUID_NotFound(t *testing.T) {
	repo := newFake()
	_, err := repo.FindByUUID(context.Background(), "nope")
	if !errors.Is(err, organizations.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
