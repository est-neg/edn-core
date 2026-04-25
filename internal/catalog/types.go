package catalog

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Item is an atomic catalog entry scoped to a tenant.
// Items are reused across packages; they should not be deleted once referenced.
type Item struct {
	ID             primitive.ObjectID     `bson:"_id,omitempty"`
	ItemUUID       string                 `bson:"item_uuid"`
	OrganizationID string                 `bson:"organization_id"`
	TenantID       string                 `bson:"tenant_id"`
	Type           string                 `bson:"type"` // product | service
	Slug           string                 `bson:"slug"`
	Name           string                 `bson:"name"`
	Description    string                 `bson:"description,omitempty"`
	DeliveryMode   string                 `bson:"delivery_mode"` // digital | physical | api_grant
	Active         bool                   `bson:"active"`
	Metadata       map[string]interface{} `bson:"metadata,omitempty"`
	CreatedAt      time.Time              `bson:"created_at"`
	UpdatedAt      time.Time              `bson:"updated_at"`
}
