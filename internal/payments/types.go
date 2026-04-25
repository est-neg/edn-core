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
type CreateCheckoutRequest struct {
	OrganizationSlug string          `json:"organization_slug,omitempty"`
	TenantSlug       string          `json:"tenant_slug,omitempty"`
	Channel          string          `json:"channel,omitempty"`
	PlanSlug         string          `json:"plan_slug"`
	BillingCycle     string          `json:"billing_cycle"`
	Customer         CustomerPayload `json:"customer"`
	IdempotencyKey   string          `json:"-"`
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

// CreateCheckoutResponse is the HTTP response for POST /v1/checkout/sessions.
type CreateCheckoutResponse struct {
	OrderNSU    string      `json:"order_nsu"`
	Status      OrderStatus `json:"status"`
	CheckoutURL string      `json:"checkout_url"`
	ExpiresAt   time.Time   `json:"expires_at"`
}

// OrderStatusResponse is the HTTP response for GET /v1/orders/{orderNSU}/status.
type OrderStatusResponse struct {
	OrderNSU           string             `json:"order_nsu"`
	Status             OrderStatus        `json:"status"`
	SubscriptionStatus SubscriptionStatus `json:"subscription_status,omitempty"`
	ReceiptURL         string             `json:"receipt_url,omitempty"`
	PlanSlug           string             `json:"plan_slug"`
}

// PlanResponse is a single plan in GET /v1/plans.
type PlanResponse struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	BillingCycle string `json:"billing_cycle"`
	PriceCents   int64  `json:"price_cents"`
	Currency     string `json:"currency"`
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
