package organizations

import "errors"

var (
	// ErrNotFound is returned when an organization cannot be located.
	ErrNotFound = errors.New("organizations: not found")

	// ErrDuplicate is returned when an organization with the same slug or uuid already exists.
	ErrDuplicate = errors.New("organizations: duplicate")

	// ErrInactive is returned when the organization exists but is not active.
	ErrInactive = errors.New("organizations: inactive")
)
