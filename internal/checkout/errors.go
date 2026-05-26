package checkout

import "errors"

var (
	// ErrOrderNotFound is returned when an order cannot be located by its NSU.
	ErrOrderNotFound = errors.New("checkout: order not found")

	// ErrPaymentNotFound is returned when a payment cannot be located by its transaction NSU.
	ErrPaymentNotFound = errors.New("checkout: payment not found")

	// ErrSubscriptionNotFound is returned when no subscription matches the given criteria.
	ErrSubscriptionNotFound = errors.New("checkout: subscription not found")

	// ErrDuplicateOrder is returned when an order with the same order_nsu already exists.
	ErrDuplicateOrder = errors.New("checkout: duplicate order")

	// ErrDuplicateWebhook is returned when a webhook event with the same event_hash already exists.
	ErrDuplicateWebhook = errors.New("checkout: duplicate webhook event")

	// ErrLockNotAcquired is returned when the distributed order lock could not be acquired.
	ErrLockNotAcquired = errors.New("checkout: order lock not acquired")

	// ErrDuplicateOutbox is returned when an outbox event with the same event_id already exists.
	// Callers may treat this as a successful no-op (idempotent retry).
	ErrDuplicateOutbox = errors.New("checkout: duplicate outbox event")
)
