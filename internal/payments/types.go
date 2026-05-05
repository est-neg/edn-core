package payments

import "time"

// OrderStatus represents the lifecycle status of a checkout order.
type OrderStatus string

const (
	OrderStatusCreated         OrderStatus = "created"
	OrderStatusCheckoutCreated OrderStatus = "checkout_created"
	OrderStatusPending         OrderStatus = "pending"
	OrderStatusPaid            OrderStatus = "paid"
	OrderStatusFailed          OrderStatus = "failed"
	OrderStatusExpired         OrderStatus = "expired"
	OrderStatusPendingReview   OrderStatus = "pending_review"
)

// PaymentStatus represents the lifecycle status of a payment transaction.
type PaymentStatus string

const (
	PaymentStatusPending       PaymentStatus = "pending"
	PaymentStatusApproved      PaymentStatus = "approved"
	PaymentStatusRejected      PaymentStatus = "rejected"
	PaymentStatusRefunded      PaymentStatus = "refunded"
	PaymentStatusChargeback    PaymentStatus = "chargeback"
	PaymentStatusPendingReview PaymentStatus = "pending_review"
)

// SubscriptionStatus represents the lifecycle status of a subscription.
type SubscriptionStatus string

const (
	SubscriptionStatusPendingActivation SubscriptionStatus = "pending_activation"
	SubscriptionStatusActive            SubscriptionStatus = "active"
	SubscriptionStatusSuspended         SubscriptionStatus = "suspended"
	SubscriptionStatusCanceled          SubscriptionStatus = "canceled"
	SubscriptionStatusExpired           SubscriptionStatus = "expired"
)

// CreateCheckoutRequest is the inbound HTTP request for POST /v1/checkout/sessions.
// Sending a non-empty checkout_intent_key activates create-or-resume semantics:
// the backend looks up the existing checkout by that handle instead of creating a new one.
type CreateCheckoutRequest struct {
	OrganizationSlug string          `json:"organization_slug,omitempty"`
	TenantSlug       string          `json:"tenant_slug,omitempty"`
	Channel          string          `json:"channel,omitempty"`
	PlanSlug         string          `json:"plan_slug"`
	BillingCycle     string          `json:"billing_cycle"`
	Customer         CustomerPayload `json:"customer"`
	// CheckoutIntentKey is the backend-issued opaque resume handle from a prior 201 response.
	// When present, the endpoint attempts to resume the existing checkout rather than create a new one.
	CheckoutIntentKey string `json:"checkout_intent_key,omitempty"`
	IdempotencyKey    string `json:"-"`
}

// PlanListQuery is the inbound query contract for GET /v1/plans.
type PlanListQuery struct {
	OrganizationSlug string
	TenantSlug       string
	Channel          string
}

// CustomerPayload holds the customer fields nested in CreateCheckoutRequest.
type CustomerPayload struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Document string `json:"document"` // CPF — 11 digits, com ou sem pontuação
}

// CreateCheckoutResponse is the canonical success payload for POST /v1/checkout/sessions.
// Returned for both created (201) and resumed (200) outcomes.
type CreateCheckoutResponse struct {
	OrderNSU string `json:"order_nsu"`
	// CheckoutIntentKey is the backend-issued opaque handle clients must persist for resume flows.
	CheckoutIntentKey string      `json:"checkout_intent_key,omitempty"`
	Status            OrderStatus `json:"status"`
	CheckoutURL       string      `json:"checkout_url"`
	// ExpiresAt is backend-authoritative. Persisted at order creation.
	ExpiresAt time.Time `json:"expires_at"`
	// Resumed is an internal flag used by the handler to emit 200 instead of 201.
	Resumed bool `json:"-"`
}

// CheckoutErrorResponse is the structured error body for 409 and 503 responses.
type CheckoutErrorResponse struct {
	Error     string `json:"error"`
	ErrorCode string `json:"error_code"`
}

// OrderStatusResponse is the HTTP response for GET /v1/orders/{orderNSU}/status.
type OrderStatusResponse struct {
	OrderNSU           string             `json:"order_nsu"`
	Status             OrderStatus        `json:"status"`
	SubscriptionStatus SubscriptionStatus `json:"subscription_status,omitempty"`
	ReceiptURL         string             `json:"receipt_url,omitempty"`
	PlanSlug           string             `json:"plan_slug"`
}

// TrackCheckoutRequest is the inbound body for POST /v1/checkout/sessions/track.
// Uses POST to keep the token out of URL/path/query/logs.
type TrackCheckoutRequest struct {
	CheckoutIntentKey string `json:"checkout_intent_key"`
}

// TrackCheckoutResponse is the public-safe tracking payload for POST /v1/checkout/sessions/track.
// checkout_url is present only when the order is resumable and has a confirmed provider URL.
// receipt_url is present only when the order is paid.
// No customer PII is returned.
type TrackCheckoutResponse struct {
	OrderNSU          string      `json:"order_nsu"`
	CheckoutIntentKey string      `json:"checkout_intent_key"`
	Status            OrderStatus `json:"status"`
	PlanSlug          string      `json:"plan_slug"`
	ExpiresAt         time.Time   `json:"expires_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
	Resumable         bool        `json:"resumable"`
	CheckoutURL       string      `json:"checkout_url,omitempty"`
	ReceiptURL        string      `json:"receipt_url,omitempty"`
}

// AdminOrderSearchRequest is the inbound body for POST /api/v1/orders/search.
// CPF is sent in the body to keep it out of URL/path/query/logs.
type AdminOrderSearchRequest struct {
	Document string `json:"document"`
}

// AdminOrderSummary is one operational order entry in AdminOrderSearchResponse.
type AdminOrderSummary struct {
	OrderNSU     string      `json:"order_nsu"`
	Status       OrderStatus `json:"status"`
	PlanSlug     string      `json:"plan_slug"`
	BillingCycle string      `json:"billing_cycle"`
	AmountCents  int64       `json:"amount_cents"`
	Currency     string      `json:"currency"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	ExpiresAt    *time.Time  `json:"expires_at,omitempty"`
}

// AdminOrderSearchResponse is the payload for POST /api/v1/orders/search.
type AdminOrderSearchResponse struct {
	Orders []AdminOrderSummary `json:"orders"`
}

// PlanResponse is a single plan in GET /v1/plans.
type PlanResponse struct {
	ID              string `json:"id"`
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	BillingCycle    string `json:"billing_cycle"`
	PriceCents      int64  `json:"price_cents"`
	Currency        string `json:"currency"`
	Active          bool   `json:"active"`
	MaxInstallments int    `json:"max_installments"`
}

// VerifyPaymentRequest is the request body for POST /internal/payments/verify.
type VerifyPaymentRequest struct {
	OrderNSU       string `json:"order_nsu"`
	TransactionNSU string `json:"transaction_nsu,omitempty"`
	InvoiceSlug    string `json:"invoice_slug,omitempty"`
}

// VerifyPaymentResponse is the response for POST /internal/payments/verify.
type VerifyPaymentResponse struct {
	OrderNSU        string        `json:"order_nsu"`
	PaymentStatus   PaymentStatus `json:"payment_status"`
	OrderStatus     OrderStatus   `json:"order_status"`
	PaidAmountCents int64         `json:"paid_amount_cents"`
	ReceiptURL      string        `json:"receipt_url,omitempty"`
}

// PaymentApprovedEvent is the Pub/Sub event payload for payment.approved.
type PaymentApprovedEvent struct {
	EventID         string    `json:"event_id"`
	OrderNSU        string    `json:"order_nsu"`
	TransactionNSU  string    `json:"transaction_nsu"`
	PlanSlug        string    `json:"plan_slug"`
	PlanID          string    `json:"plan_id"`
	AmountCents     int64     `json:"amount_cents"`
	PaidAmountCents int64     `json:"paid_amount_cents"`
	CustomerEmail   string    `json:"customer_email"`
	OccurredAt      time.Time `json:"occurred_at"`
}

// InfinitePayCheckoutRequest is the payload sent to InfinitePay's checkout API.
type InfinitePayCheckoutRequest struct {
	Handle           string `json:"handle"`
	OrderNSU         string `json:"order_nsu"`
	PlanName         string `json:"plan_name"`
	AmountCents      int64  `json:"amount_cents"`
	Currency         string `json:"currency"`
	MaxInstallments  int    `json:"max_installments"`
	CustomerName     string `json:"customer_name"`
	CustomerEmail    string `json:"customer_email"`
	CustomerPhone    string `json:"customer_phone"`
	CustomerDocument string `json:"customer_document"` // CPF normalizado (11 dígitos)
	WebhookURL       string `json:"webhook_url"`
	RedirectURL      string `json:"redirect_url"`
}

// InfinitePayCheckoutResponse is the parsed response from InfinitePay checkout creation.
type InfinitePayCheckoutResponse struct {
	CheckoutURL string `json:"checkout_url"`
	InvoiceSlug string `json:"invoice_slug,omitempty"`
}

// InfinitePayVerifyResponse is the parsed response from InfinitePay payment verification.
type InfinitePayVerifyResponse struct {
	ProviderStatus  string        `json:"status"`
	Status          PaymentStatus `json:"-"` // mapped from ProviderStatus
	PaidAmountCents int64         `json:"paid_amount_cents"`
	Currency        string        `json:"currency"`
	ReceiptURL      string        `json:"receipt_url,omitempty"`
	TransactionNSU  string        `json:"transaction_nsu"`
}
