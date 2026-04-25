package tenants

import "context"

// Repository defines persistence operations for tenants.
type Repository interface {
	// Create inserts a new tenant. Returns ErrDuplicate if (organization_id, slug) collides.
	Create(ctx context.Context, tenant Tenant) error
	// FindByUUID returns a tenant by its tenant_uuid.
	FindByUUID(ctx context.Context, tenantUUID string) (*Tenant, error)
	// FindByOrgAndSlug resolves a tenant within an organization by slug.
	// Returns ErrNotFound when no match exists.
	FindByOrgAndSlug(ctx context.Context, organizationID, slug string) (*Tenant, error)
	// ListByOrg returns all tenants belonging to an organization.
	ListByOrg(ctx context.Context, organizationID string) ([]Tenant, error)
}
