package payments

import (
	"errors"
	"fmt"
	"strings"
)

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

	// ErrTransactionMismatch is returned when the provider-verified transaction ID
	// does not match the webhook hint or an already-persisted canonical transaction_nsu.
	// This is a hard integrity gate — no payment or order mutation is permitted.
	ErrTransactionMismatch = errors.New("payments: transaction identity mismatch")

	// ErrLockConflict is returned when distributed order lock acquisition fails due to
	// a concurrent reconcile attempt. This is a temporary failure that must map to 5xx
	// so that the relay returns provider-facing 400 for retry.
	ErrLockConflict = errors.New("payments: lock acquisition conflict")

	// ErrCheckoutAlreadyOpen is returned on public create when an active open order
	// with the same tenant+CPF+plan fingerprint already exists. No session data is
	// leaked. Maps to 409.
	ErrCheckoutAlreadyOpen = errors.New("payments: checkout already open")

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

type checkoutContinuationError struct {
	err               error
	orderNSU          string
	checkoutIntentKey string
}

func (e *checkoutContinuationError) Error() string {
	return e.err.Error()
}

func (e *checkoutContinuationError) Unwrap() error {
	return e.err
}

func newCheckoutContinuationError(err error, orderNSU, checkoutIntentKey string) error {
	if err == nil {
		return nil
	}
	orderNSU = strings.TrimSpace(orderNSU)
	checkoutIntentKey = strings.TrimSpace(checkoutIntentKey)
	if orderNSU == "" && checkoutIntentKey == "" {
		return err
	}
	return &checkoutContinuationError{
		err:               err,
		orderNSU:          orderNSU,
		checkoutIntentKey: checkoutIntentKey,
	}
}

// ProviderCreateRejectedError is returned by InfinitePayClient.CreateCheckout when the
// provider responds with a deterministic rejection status (400, 401, 402, 403, 404, 422).
// Callers that detect this error must terminalize the in-flight order rather than leaving it open.
type ProviderCreateRejectedError struct {
	StatusCode int
}

func (e *ProviderCreateRejectedError) Error() string {
	return fmt.Sprintf("payments: provider rejected checkout creation with status %d", e.StatusCode)
}

// isDeterministicProviderCreateStatus reports whether an HTTP status from a provider
// checkout-create call represents a permanent, non-retriable rejection.
// 408, 429, and 5xx are excluded — they are transient or ambiguous.
func isDeterministicProviderCreateStatus(code int) bool {
	switch code {
	case 400, 401, 402, 403, 404, 422:
		return true
	}
	return false
}

// Stable error_code values returned in API responses for 409 and 503.
const (
	// Technical idempotency / concurrency conflicts — not resume-state errors.
	ErrCodeCheckoutIdempotencyConflict = "checkout_idempotency_conflict"
	ErrCodeCheckoutInProgress          = "checkout_in_progress"
	ErrCodeCheckoutAlreadyOpen         = "checkout_already_open"

	// Resume-state conflicts.
	ErrCodeCheckoutNonResumable = "checkout_non_resumable"
	ErrCodeCheckoutExpired      = "checkout_expired"

	// Provider / infrastructure errors.
	ErrCodeCheckoutRecoveryRequired = "checkout_recovery_required"
	ErrCodeProviderStateAmbiguous   = "provider_state_ambiguous"
)
