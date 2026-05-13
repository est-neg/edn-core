package whatsapp

import "context"

// Repository persists message receipts and dedupe records for the WhatsApp bounded context.
type Repository interface {
	// InsertMessageWithDedupe atomically inserts a dedupe record and a receipt.
	// Returns ErrDuplicate when dedupe_key already exists (safe to skip and continue).
	// Any other error indicates a transient failure that should surface as 503.
	InsertMessageWithDedupe(ctx context.Context, dedupe *DedupeRecord, receipt *MessageReceipt) error
}
