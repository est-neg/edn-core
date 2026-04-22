package payments

import "errors"

var (
	ErrPlanNotFound   = errors.New("payments: plan not found")
	ErrPlanInactive   = errors.New("payments: plan is not active")
	ErrOrderNotFound  = errors.New("payments: order not found")
	ErrAmountMismatch = errors.New("payments: paid amount does not match expected")
	ErrInvalidRequest = errors.New("payments: invalid request")
)
