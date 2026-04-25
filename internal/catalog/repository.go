package catalog

import (
	"context"
	"time"
)

// Repository defines persistence operations for catalog items.
type Repository interface {
	// Create inserts a new catalog item. Returns ErrDuplicate if (tenant_id, slug) collides.
	Create(ctx context.Context, item Item) error
	// FindByUUID returns a catalog item by its item_uuid.
	FindByUUID(ctx context.Context, itemUUID string) (*Item, error)
	// FindByTenantAndSlug returns a catalog item by its (tenant_id, slug).
	FindByTenantAndSlug(ctx context.Context, tenantID, slug string) (*Item, error)
	// ListByTenant returns active catalog items for a tenant, optionally filtered by type.
	// Pass an empty string for itemType to return all types.
	ListByTenant(ctx context.Context, tenantID, itemType string) ([]Item, error)
	// Deactivate marks a catalog item as inactive.
	Deactivate(ctx context.Context, itemUUID string, updatedAt time.Time) error
}
