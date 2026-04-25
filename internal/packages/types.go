package packages

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PackageItem is a snapshot of a catalog item included in a package.
// The snapshot captures the item's identity at the time the package version is created;
// it does not follow future changes to the catalog item.
type PackageItem struct {
	ItemUUID     string `bson:"item_uuid"`
	ItemType     string `bson:"item_type"`
	ItemSlug     string `bson:"item_slug"`
	ItemName     string `bson:"item_name"`
	Quantity     int    `bson:"quantity"`
	DeliveryMode string `bson:"delivery_mode"`
}

// Package is a versioned composition of catalog items scoped to a tenant.
// Changing the composition creates a new version; the previous version becomes immutable.
type Package struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"`
	PackageUUID    string             `bson:"package_uuid"`
	OrganizationID string             `bson:"organization_id"`
	TenantID       string             `bson:"tenant_id"`
	Slug           string             `bson:"slug"`
	Version        int                `bson:"version"`
	Name           string             `bson:"name"`
	Status         string             `bson:"status"` // draft | published | archived
	Items          []PackageItem      `bson:"items"`
	CreatedAt      time.Time          `bson:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at"`
}
