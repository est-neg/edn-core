package leads

import "errors"

// ErrDuplicateLead is returned when the same whatsapp+profile was submitted
// within the configured deduplication window.
var ErrDuplicateLead = errors.New("duplicate_lead")

// ErrInvalidPayload is the sentinel used to test whether an error originates
// from payload validation. Wrap ValidationError with this target.
var ErrInvalidPayload = errors.New("invalid_payload")

// ValidationError carries a human-readable detail message for 400 responses.
type ValidationError struct {
	Details string
}

func (e *ValidationError) Error() string {
	return "invalid_payload: " + e.Details
}

// Is makes errors.Is(err, ErrInvalidPayload) return true for any *ValidationError.
func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidPayload
}
