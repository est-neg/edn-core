package organizations

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Organization is the root administrative and billing entity.
// It owns one or more tenants.
type Organization struct {
	ID           primitive.ObjectID `bson:"_id,omitempty"`
	OrgUUID      string             `bson:"org_uuid"`
	Slug         string             `bson:"slug"`
	Name         string             `bson:"name"`
	BillingEmail string             `bson:"billing_email"`
	Active       bool               `bson:"active"`
	CreatedAt    time.Time          `bson:"created_at"`
	UpdatedAt    time.Time          `bson:"updated_at"`
}
