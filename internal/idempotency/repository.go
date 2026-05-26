package idempotency

import (
	"context"
	"time"
)

// Repository defines persistence operations for idempotency keys.
//
// Usage pattern for a safe write operation:
//  1. Call Reserve to insert a pending record.
//     - If ErrDuplicate is returned, call FindByTenantOpKey to get the existing record.
//     - If the existing record has a different request_hash, return ErrRequestHashConflict to the caller.
//     - If the existing record is committed, return the recorded resource_id.
//  2. Perform the operation (e.g. create the order).
//  3. Call Commit (in the same Mongo session/transaction if possible).
type Repository interface {
	// Reserve inserts a new idempotency record with status=pending.
	// Returns ErrDuplicate if the (tenant_id, operation, idempotency_key) already exists.
	Reserve(ctx context.Context, key Key) error

	// FindByTenantOpKey looks up an existing record by its natural composite key.
	// Returns ErrNotFound when no record exists.
	FindByTenantOpKey(ctx context.Context, tenantID, operation, idempotencyKey string) (*Key, error)

	// FindByTenantOpResourceID looks up an existing record by tenant, operation, and the persisted resource_id.
	// Returns ErrNotFound when no record exists.
	FindByTenantOpResourceID(ctx context.Context, tenantID, operation, resourceID string) (*Key, error)

	// Commit updates the record to status=committed and records the durable replay snapshot.
	Commit(ctx context.Context, tenantID, operation, idempotencyKey, resourceID, resourceStatus, resourceURL, externalRef string, updatedAt time.Time) error

	// Fail updates the record to status=failed so the key cannot silently be retried.
	Fail(ctx context.Context, tenantID, operation, idempotencyKey string, updatedAt time.Time) error
}
