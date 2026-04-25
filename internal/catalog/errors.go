package catalog

import "errors"

var (
	// ErrNotFound is returned when a catalog item cannot be located.
	ErrNotFound = errors.New("catalog: item not found")

	// ErrDuplicate is returned when an item with the same (tenant_id, slug) already exists.
	ErrDuplicate = errors.New("catalog: duplicate item")
)

// Item types.
const (
	ItemTypeProduct = "product"
	ItemTypeService = "service"
)

// Delivery modes.
const (
	DeliveryModeDigital  = "digital"
	DeliveryModePhysical = "physical"
	DeliveryModeAPIGrant = "api_grant"
)
