package idempotency

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Key is the authoritative idempotency record for a single external operation.
// It prevents duplicate side-effects for checkout creation and other write operations.
//
// Lifecycle:
//   - Reserve: insert with status=pending, recording key+request_hash before calling the provider.
//   - Commit:  update to status=committed with resource_id once the operation succeeds.
//   - Fail:    update to status=failed so the key may not be silently retried.
//
// Conflict rules:
//   - same key + same request_hash → idempotent replay; return the committed resource_id.
//   - same key + different request_hash → ErrRequestHashConflict; reject the request.
type Key struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"`
	Operation      string             `bson:"operation"`       // e.g. "checkout.create"
	OrganizationID string             `bson:"organization_id"` // references Organization.OrgUUID
	TenantID       string             `bson:"tenant_id"`       // references Tenant.TenantUUID
	IdempotencyKey string             `bson:"idempotency_key"` // client-supplied opaque key
	RequestHash    string             `bson:"request_hash"`    // SHA-256 of canonical request body
	ResourceType   string             `bson:"resource_type"`   // e.g. "order"
	ResourceID     string             `bson:"resource_id"`     // set on commit; e.g. order_nsu
	ResourceStatus string             `bson:"resource_status,omitempty"`
	ResourceURL    string             `bson:"resource_url,omitempty"`
	ExternalRef    string             `bson:"external_ref,omitempty"`
	Status         string             `bson:"status"`     // pending | committed | failed
	Expirable      bool               `bson:"expirable"`  // only expirable records participate in TTL cleanup
	ExpiresAt      time.Time          `bson:"expires_at"` // TTL — managed by MongoDB TTL index
	CreatedAt      time.Time          `bson:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at"`
}
