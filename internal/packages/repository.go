package packages

import "context"

// Repository defines persistence operations for versioned packages.
type Repository interface {
	// Create inserts a new package version.
	// Returns ErrDuplicate if (tenant_id, slug, version) already exists.
	Create(ctx context.Context, pkg Package) error
	// FindByUUID returns a package by its package_uuid.
	FindByUUID(ctx context.Context, packageUUID string) (*Package, error)
	// FindByTenantSlugVersion returns a specific version of a package.
	FindByTenantSlugVersion(ctx context.Context, tenantID, slug string, version int) (*Package, error)
	// FindLatestPublishedBySlug returns the highest-version published package for a slug.
	FindLatestPublishedBySlug(ctx context.Context, tenantID, slug string) (*Package, error)
}
