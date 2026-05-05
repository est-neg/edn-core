package payments

import "errors"

var (
	ErrPlanNotFound         = errors.New("payments: plan not found")
	ErrPlanInactive         = errors.New("payments: plan is not active")
	ErrOrganizationNotFound = errors.New("payments: organization not found")
	ErrTenantNotFound       = errors.New("payments: tenant not found")
	ErrOrderNotFound        = errors.New("payments: order not found")
	ErrAmountMismatch       = errors.New("payments: paid amount does not match expected")
	ErrInvalidRequest       = errors.New("payments: invalid request")
	ErrCheckoutConflict     = errors.New("payments: checkout idempotency conflict")
	ErrCheckoutInProgress   = errors.New("payments: checkout already in progress")

	// ErrCheckoutNonResumable is returned when the referenced checkout exists but
	// is in a terminal or otherwise non-resumable state. Maps to 409.
	ErrCheckoutNonResumable = errors.New("payments: checkout not resumable")

	// ErrCheckoutExpired is returned when the checkout_intent_key is valid but
	// the backend-authoritative expires_at has passed. Maps to 409.
	ErrCheckoutExpired = errors.New("payments: checkout expired")

	// ErrCheckoutRecoveryRequired is returned when the order exists but
	// provider_checkout_url is missing and repair is not possible synchronously.
	// Client must not be given a false-success response. Maps to 503.
	ErrCheckoutRecoveryRequired = errors.New("payments: checkout recovery required")

	// ErrProviderStateAmbiguous is returned when the backend cannot determine
	// which provider session is canonical. Maps to 503.
	ErrProviderStateAmbiguous = errors.New("payments: provider state ambiguous")
)

// Stable error_code values returned in API responses for 409 and 503.
const (
	// Technical idempotency / concurrency conflicts — not resume-state errors.
	ErrCodeCheckoutIdempotencyConflict = "checkout_idempotency_conflict"
	ErrCodeCheckoutInProgress          = "checkout_in_progress"

	// Resume-state conflicts.
	ErrCodeCheckoutNonResumable = "checkout_non_resumable"
	ErrCodeCheckoutExpired      = "checkout_expired"

	// Provider / infrastructure errors.
	ErrCodeCheckoutRecoveryRequired = "checkout_recovery_required"
	ErrCodeProviderStateAmbiguous   = "provider_state_ambiguous"
)
