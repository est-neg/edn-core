package plans

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Billing cycle values for Plan.BillingCycle.
const (
	BillingCycleMonthly = "monthly"
	BillingCycleAnnual  = "annual"
	BillingCycleOneTime = "one_time"
)

// Channel values for Plan.Channel.
const (
	ChannelWeb     = "web"
	ChannelMobile  = "mobile"
	ChannelPartner = "partner"
	ChannelAll     = "all"
)

// PackageRef is a lightweight pointer to a specific package version.
// It is stored alongside the full PackageSnapshot so queries can navigate by UUID
// without depending on the live packages collection.
type PackageRef struct {
	PackageUUID string `bson:"package_uuid"`
	Version     int    `bson:"version"`
}

// PackageSnapshot captures the package name at plan-creation time.
// Preserves historical accuracy — a plan's snapshot never follows future package edits.
type PackageSnapshot struct {
	PackageUUID string `bson:"package_uuid"`
	Name        string `bson:"name"`
	Version     int    `bson:"version"`
}

// Plan is a versioned commercial offer scoped to a tenant and organization.
// Creating a new price, cycle, or channel produces a new version; prior versions are immutable.
type Plan struct {
	ID              primitive.ObjectID `bson:"_id,omitempty"`
	PlanUUID        string             `bson:"plan_uuid"`
	OrganizationID  string             `bson:"organization_id"`
	TenantID        string             `bson:"tenant_id"`
	Slug            string             `bson:"slug"`
	Version         int                `bson:"version"`
	Name            string             `bson:"name"`
	PackageRef      PackageRef         `bson:"package_ref"`
	PackageSnapshot PackageSnapshot    `bson:"package_snapshot"`
	BillingCycle    string             `bson:"billing_cycle"` // monthly | annual | one_time
	PriceCents      int64              `bson:"price_cents"`
	Currency        string             `bson:"currency"`
	MaxInstallments int                `bson:"max_installments"`
	Channel         string             `bson:"channel"` // web | mobile | partner | all
	Active          bool               `bson:"active"`
	ValidFrom       time.Time          `bson:"valid_from"`
	ValidUntil      *time.Time         `bson:"valid_until,omitempty"`
	CreatedAt       time.Time          `bson:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at"`
}
