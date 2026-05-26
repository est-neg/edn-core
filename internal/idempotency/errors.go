package idempotency

import "errors"

var (
	// ErrNotFound is returned when no idempotency record matches the lookup.
	ErrNotFound = errors.New("idempotency: record not found")

	// ErrDuplicate is returned when the same (tenant_id, operation, key) is being reserved
	// concurrently and the insert races. The caller should look up the existing record.
	ErrDuplicate = errors.New("idempotency: duplicate key")

	// ErrRequestHashConflict is returned when the same idempotency key is reused with a
	// different request_hash. This indicates the client sent a different payload for the
	// same key, which is a protocol violation.
	ErrRequestHashConflict = errors.New("idempotency: request hash conflict")
)

// Status values for an idempotency record.
const (
	StatusPending   = "pending"
	StatusCommitted = "committed"
	StatusFailed    = "failed"
)
