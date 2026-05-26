package tenants

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Tenant is the operational isolation unit within an Organization.
// Catalog items, packages, plans, orders, and subscriptions are scoped to a tenant.
type Tenant struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"    json:"-"`
	TenantUUID     string             `bson:"tenant_uuid"      json:"tenant_uuid"`
	OrganizationID string             `bson:"organization_id"  json:"organization_id"` // references Organization.OrgUUID
	Slug           string             `bson:"slug"             json:"slug"`
	Name           string             `bson:"name"             json:"name"`
	Channels       []string           `bson:"channels"         json:"channels"` // e.g. ["web","mobile"]
	Active         bool               `bson:"active"           json:"active"`
	CreatedAt      time.Time          `bson:"created_at"       json:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at"       json:"updated_at"`
}
