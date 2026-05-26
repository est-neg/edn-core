package organizations

import "context"

// Repository defines persistence operations for organizations.
type Repository interface {
	// Create inserts a new organization. Returns ErrDuplicate if slug or org_uuid collides.
	Create(ctx context.Context, org Organization) error
	// FindByUUID returns an organization by its org_uuid.
	FindByUUID(ctx context.Context, orgUUID string) (*Organization, error)
	// FindBySlug returns an organization by its slug.
	FindBySlug(ctx context.Context, slug string) (*Organization, error)
	// List returns all organizations.
	List(ctx context.Context) ([]Organization, error)
	// Update replaces mutable fields of an existing organization.
	Update(ctx context.Context, orgUUID string, org Organization) error
	// Deactivate sets active=false on the organization.
	Deactivate(ctx context.Context, orgUUID string) error
}
