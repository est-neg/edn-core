package tenants

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Tenant is the operational isolation unit within an Organization.
// Catalog items, packages, plans, orders, and subscriptions are scoped to a tenant.
type Tenant struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"`
	TenantUUID     string             `bson:"tenant_uuid"`
	OrganizationID string             `bson:"organization_id"` // references Organization.OrgUUID
	Slug           string             `bson:"slug"`
	Name           string             `bson:"name"`
	Channels       []string           `bson:"channels"` // e.g. ["web","mobile"]
	Active         bool               `bson:"active"`
	CreatedAt      time.Time          `bson:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at"`
}
