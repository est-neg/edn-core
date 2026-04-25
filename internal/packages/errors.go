package packages

import "errors"

var (
	// ErrNotFound is returned when a package cannot be located.
	ErrNotFound = errors.New("packages: not found")

	// ErrDuplicate is returned when a package with the same (tenant_id, slug, version) already exists.
	ErrDuplicate = errors.New("packages: duplicate")

	// ErrImmutable is returned when a published package version is being mutated.
	// A new version must be created instead.
	ErrImmutable = errors.New("packages: published package version is immutable")
)

// Package status values.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusArchived  = "archived"
)
