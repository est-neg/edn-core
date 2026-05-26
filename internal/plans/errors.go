package plans

import "errors"

var (
	// ErrNotFound is returned when a plan cannot be located.
	ErrNotFound = errors.New("plans: not found")

	// ErrDuplicate is returned when a plan with the same (tenant_id, slug, version) already exists.
	ErrDuplicate = errors.New("plans: duplicate")

	// ErrInactive is returned when the plan exists but is not active.
	ErrInactive = errors.New("plans: inactive")
)
