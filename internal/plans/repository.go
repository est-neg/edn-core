package plans

import (
	"context"
	"time"
)

// Repository defines persistence operations for versioned commercial plans.
type Repository interface {
	// Create inserts a new plan version.
	// Returns ErrDuplicate if (tenant_id, slug, version) already exists.
	Create(ctx context.Context, plan Plan) error

	// FindByUUID returns a plan by its stable plan_uuid.
	// Returns ErrNotFound if not present.
	FindByUUID(ctx context.Context, planUUID string) (*Plan, error)

	// FindActiveByTenantSlug returns the highest-version active plan for a tenant and slug.
	// Returns ErrNotFound if no active plan exists for that combination.
	FindActiveByTenantSlug(ctx context.Context, tenantID, slug string) (*Plan, error)

	// FindSellableByTenantSlug returns the highest-version plan variant that is
	// active, currently valid, and sellable for the requested billing cycle and channel.
	FindSellableByTenantSlug(ctx context.Context, tenantID, slug, billingCycle, channel string, at time.Time) (*Plan, error)

	// ListActiveByTenant returns all active plans for a tenant.
	// When channel is non-empty and not "all", only plans scoped to that channel
	// or to "all" are returned. Pass an empty string to return every active plan.
	ListActiveByTenant(ctx context.Context, tenantID, channel string) ([]Plan, error)
}
