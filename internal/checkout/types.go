package checkout

import (
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Order represents a checkout order document stored in the orders collection.
type Order struct {
	ID                  primitive.ObjectID `bson:"_id,omitempty"`
	OrderID             string             `bson:"id"`
	OrderNSU            string             `bson:"order_nsu"`
	OrganizationID      string             `bson:"organization_id,omitempty"`
	TenantID            string             `bson:"tenant_id,omitempty"`
	PlanID              string             `bson:"plan_id"`
	PlanSlug            string             `bson:"plan_slug"`
	PlanVersion         int                `bson:"plan_version,omitempty"`
	PlanSource          string             `bson:"plan_source,omitempty"`
	BillingCycle        string             `bson:"billing_cycle"`
	AmountCents         int64              `bson:"amount_cents"`
	Currency            string             `bson:"currency"`
	CustomerName        string             `bson:"customer_name"`
	CustomerEmail       string             `bson:"customer_email"`
	CustomerPhone       string             `bson:"customer_phone"`
	CustomerDocument    string             `bson:"customer_document,omitempty"` // CPF normalizado (11 dígitos)
	Status              string             `bson:"status"`
	Provider            string             `bson:"provider"`
	ProviderCheckoutURL string             `bson:"provider_checkout_url,omitempty"`
	InvoiceSlug         string             `bson:"invoice_slug,omitempty"`
	ReceiptURL          string             `bson:"receipt_url,omitempty"`
	CreatedAt           time.Time          `bson:"created_at"`
	UpdatedAt           time.Time          `bson:"updated_at"`
}

// Payment represents a payment transaction document stored in the payments collection.
type Payment struct {
	ID              primitive.ObjectID `bson:"_id,omitempty"`
	PaymentID       string             `bson:"id"`
	OrderNSU        string             `bson:"order_nsu"`
	TransactionNSU  string             `bson:"transaction_nsu"`
	InvoiceSlug     string             `bson:"invoice_slug,omitempty"`
	AmountCents     int64              `bson:"amount_cents"`
	PaidAmountCents int64              `bson:"paid_amount_cents"`
	Installments    int                `bson:"installments"`
	CaptureMethod   string             `bson:"capture_method"`
	ReceiptURL      string             `bson:"receipt_url,omitempty"`
	Status          string             `bson:"status"`
	RawPayload      bson.Raw           `bson:"raw_payload,omitempty"`
	CreatedAt       time.Time          `bson:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at"`
}

// Subscription represents a customer subscription document stored in the subscriptions collection.
type Subscription struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"`
	SubscriptionID string             `bson:"id"`
	CustomerEmail  string             `bson:"customer_email"`
	PlanID         string             `bson:"plan_id"`
	PlanSlug       string             `bson:"plan_slug"`
	Status         string             `bson:"status"`
	OriginOrderNSU string             `bson:"origin_order_nsu"`
	StartsAt       time.Time          `bson:"starts_at"`
	EndsAt         time.Time          `bson:"ends_at"`
	CreatedAt      time.Time          `bson:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at"`
}

// WebhookEvent represents a raw inbound provider webhook stored in the webhook_events collection.
type WebhookEvent struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"`
	EventID        string             `bson:"id"`
	Provider       string             `bson:"provider"`
	EventHash      string             `bson:"event_hash"`
	OrderNSU       string             `bson:"order_nsu"`
	TransactionNSU string             `bson:"transaction_nsu"`
	Status         string             `bson:"status"`
	RawPayload     bson.Raw           `bson:"raw_payload"`
	ReceivedAt     time.Time          `bson:"received_at"`
	ProcessedAt    *time.Time         `bson:"processed_at,omitempty"`
}

// OutboxEvent represents a domain event pending reliable publication via the outbox pattern.
type OutboxEvent struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`
	EventID     string             `bson:"id"`
	EventType   string             `bson:"event_type"`
	Payload     bson.Raw           `bson:"payload"`
	Published   bool               `bson:"published"`
	CreatedAt   time.Time          `bson:"created_at"`
	PublishedAt *time.Time         `bson:"published_at,omitempty"`
}
