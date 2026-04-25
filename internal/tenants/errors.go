package tenants

import "errors"

var (
	// ErrNotFound is returned when a tenant cannot be located.
	ErrNotFound = errors.New("tenants: not found")

	// ErrDuplicate is returned when a tenant with the same (organization_id, slug) already exists.
	ErrDuplicate = errors.New("tenants: duplicate")

	// ErrInactive is returned when the tenant exists but is not active.
	ErrInactive = errors.New("tenants: inactive")

	// ErrOrganizationMismatch is returned when the tenant does not belong to the expected organization.
	ErrOrganizationMismatch = errors.New("tenants: organization mismatch")
)
