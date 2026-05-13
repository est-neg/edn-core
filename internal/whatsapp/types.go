// Package whatsapp implements the Meta WhatsApp webhook bounded context (phase 1).
// It handles public webhook verification/relay and internal message intake + durable persistence.
package whatsapp

import "time"

// MetaWebhookPayload is the top-level envelope sent by the Meta WhatsApp Cloud API.
type MetaWebhookPayload struct {
	Object string      `json:"object"`
	Entry  []MetaEntry `json:"entry"`
}

// MetaEntry is one entry in the payload.
type MetaEntry struct {
	ID      string       `json:"id"`
	Changes []MetaChange `json:"changes"`
}

// MetaChange is one change event within an entry.
type MetaChange struct {
	Value MetaChangeValue `json:"value"`
	Field string          `json:"field"`
}

// MetaChangeValue holds the content of a change event.
// Phase 1 processes only Messages; other fields (Statuses, Contacts) are ignored.
type MetaChangeValue struct {
	MessagingProduct string        `json:"messaging_product"`
	Metadata         MetaMetadata  `json:"metadata"`
	Messages         []MetaMessage `json:"messages"`
}

// MetaMetadata contains identifiers for the originating phone number.
type MetaMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

// MetaMessage is a single incoming WhatsApp message.
// Phase 1 extracts only the minimal fields required for receipt and dedupe.
type MetaMessage struct {
	From      string `json:"from"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
}

// MessageReceipt is the durable record stored for each accepted WhatsApp message.
// Phase 1 minimum: no raw_payload or raw_message — only the fields required for
// dedupe, audit, and receipt. Raw content must not be persisted without a
// retention policy in place.
type MessageReceipt struct {
	ReceiptID        string    `bson:"receipt_id"`
	Provider         string    `bson:"provider"`
	MessageID        string    `bson:"message_id"`
	PhoneNumberID    string    `bson:"phone_number_id"`
	From             string    `bson:"from"`
	MessageType      string    `bson:"message_type"`
	ReceivedAt       time.Time `bson:"received_at"`
	PayloadHash      string    `bson:"payload_hash"`
	DedupeKey        string    `bson:"dedupe_key"`
	IngressRequestID string    `bson:"ingress_request_id"`
}

// DedupeRecord is stored to prevent duplicate message processing within the 7-day window.
type DedupeRecord struct {
	DedupeKey     string    `bson:"dedupe_key"`
	MessageID     string    `bson:"message_id"`
	PhoneNumberID string    `bson:"phone_number_id"`
	CreatedAt     time.Time `bson:"created_at"`
	ExpiresAt     time.Time `bson:"expires_at"`
}

// IntakeInput holds all data required to process an incoming webhook notification batch.
type IntakeInput struct {
	IngressRequestID string
	ReceivedAt       time.Time
	RawPayload       []byte
	Payload          *MetaWebhookPayload
}
