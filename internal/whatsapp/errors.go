package whatsapp

import "errors"

// ErrDuplicate is returned when a message has already been processed within the dedupe window.
var ErrDuplicate = errors.New("whatsapp: duplicate message")

// ErrInvalidPayload is returned when a required canonical field (phone_number_id or message.id)
// is missing, making it impossible to build a valid dedupe key.
var ErrInvalidPayload = errors.New("whatsapp: invalid payload")
