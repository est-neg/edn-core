package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// infinitePayAdapter implements InfinitePayClient using InfinitePay's REST API.
// It never logs the API token, raw response bodies, or customer PII.
type infinitePayAdapter struct {
	baseURL  string
	apiToken string
	client   *http.Client
	log      *zap.Logger
}

// NewInfinitePayAdapter constructs an InfinitePayClient backed by the real provider API.
func NewInfinitePayAdapter(baseURL, apiToken string, timeoutSec int, log *zap.Logger) InfinitePayClient {
	return &infinitePayAdapter{
		baseURL:  baseURL,
		apiToken: apiToken,
		client:   &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		log:      log,
	}
}

// infinitePayLinksPayload is the wire format for POST /links (InfinitePay checkout creation).
type infinitePayLinksPayload struct {
	Handle      string              `json:"handle"`
	OrderID     string              `json:"order_id,omitempty"`
	Items       []infinitePayItem   `json:"items"`
	RedirectURL string              `json:"redirect_url"`
	WebhookURL  string              `json:"webhook_url"`
	Customer    infinitePayCustomer `json:"customer"`
}

type infinitePayCustomer struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
}

type infinitePayItem struct {
	Quantity    int    `json:"quantity"`
	Price       int64  `json:"price"` // in cents
	Description string `json:"description"`
}

// infinitePayLinksResponse uses tolerant field names to handle provider variations.
type infinitePayLinksResponse struct {
	// URL variants — provider may return any of these
	CheckoutURL string `json:"checkout_url"`
	Link        string `json:"link"`
	URL         string `json:"url"`
	// ID variants
	InvoiceID string `json:"invoice_id"`
	ID        string `json:"id"`
}

// infinitePayPaymentCheckPayload is the wire format for POST /payment_check.
type infinitePayPaymentCheckPayload struct {
	InvoiceID     string `json:"invoice_id,omitempty"`
	OrderID       string `json:"order_id,omitempty"`
	TransactionID string `json:"transaction_id,omitempty"`
}

// infinitePayPaymentCheckResponse is the parsed response from POST /payment_check.
type infinitePayPaymentCheckResponse struct {
	Status          string `json:"status"`
	TransactionID   string `json:"transaction_id"`
	PaidAmountCents int64  `json:"paid_amount_cents"`
	Currency        string `json:"currency"`
	ReceiptURL      string `json:"receipt_url"`
}

func (a *infinitePayAdapter) CreateCheckout(ctx context.Context, req InfinitePayCheckoutRequest) (InfinitePayCheckoutResponse, error) {
	payload := infinitePayLinksPayload{
		Handle:      req.Handle,
		OrderID:     req.OrderNSU,
		RedirectURL: req.RedirectURL,
		WebhookURL:  req.WebhookURL,
		Customer: infinitePayCustomer{
			Name:        req.CustomerName,
			Email:       req.CustomerEmail,
			PhoneNumber: req.CustomerPhone,
		},
		Items: []infinitePayItem{
			{
				Quantity:    1,
				Price:       req.AmountCents,
				Description: req.PlanName,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("marshal checkout payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/links", bytes.NewReader(body))
	if err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("build checkout request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// POST /links does not use Authorization — the merchant handle embedded in the
	// payload identifies the account. Do not send the API token here.

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("provider checkout http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Log status code only — not response body (may contain sensitive data)
		a.log.Warn("provider checkout non-2xx", zap.Int("status", resp.StatusCode))
		if isDeterministicProviderCreateStatus(resp.StatusCode) {
			return InfinitePayCheckoutResponse{}, &ProviderCreateRejectedError{StatusCode: resp.StatusCode}
		}
		return InfinitePayCheckoutResponse{}, fmt.Errorf("provider checkout status %d", resp.StatusCode)
	}

	var providerResp infinitePayLinksResponse
	if err := json.Unmarshal(respBody, &providerResp); err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("decode checkout response: %w", err)
	}

	// Tolerant field resolution: accept checkout_url, link, or url
	checkoutURL := providerResp.CheckoutURL
	if checkoutURL == "" {
		checkoutURL = providerResp.Link
	}
	if checkoutURL == "" {
		checkoutURL = providerResp.URL
	}

	// Guard: URL must be present after tolerant resolution
	if checkoutURL == "" {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("provider checkout missing checkout url")
	}

	// Tolerant ID resolution: accept invoice_id or id
	invoiceID := providerResp.InvoiceID
	if invoiceID == "" {
		invoiceID = providerResp.ID
	}

	return InfinitePayCheckoutResponse{
		CheckoutURL: checkoutURL,
		InvoiceSlug: invoiceID,
	}, nil
}

func (a *infinitePayAdapter) VerifyPayment(ctx context.Context, invoiceSlug, orderNSU, transactionNSU string) (InfinitePayVerifyResponse, error) {
	payload := infinitePayPaymentCheckPayload{
		InvoiceID:     invoiceSlug,
		OrderID:       orderNSU,
		TransactionID: transactionNSU,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return InfinitePayVerifyResponse{}, fmt.Errorf("marshal verify payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/payment_check", bytes.NewReader(body))
	if err != nil {
		return InfinitePayVerifyResponse{}, fmt.Errorf("build verify request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return InfinitePayVerifyResponse{}, fmt.Errorf("provider verify http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		a.log.Warn("provider verify non-2xx", zap.Int("status", resp.StatusCode))
		return InfinitePayVerifyResponse{}, fmt.Errorf("provider verify status %d", resp.StatusCode)
	}

	var providerResp infinitePayPaymentCheckResponse
	if err := json.Unmarshal(respBody, &providerResp); err != nil {
		return InfinitePayVerifyResponse{}, fmt.Errorf("decode verify response: %w", err)
	}

	// Guard: canonical transaction ID must be present in the verify response.
	// A missing transaction_id means payment_check cannot provide reconciliation identity.
	// Accepting an empty value here would allow the webhook hint to silently become canonical
	// downstream — a direct violation of the Story 4 invariant.
	if providerResp.TransactionID == "" {
		return InfinitePayVerifyResponse{}, fmt.Errorf("provider verify response missing transaction_id: cannot reconcile payment without canonical transaction identity")
	}

	return InfinitePayVerifyResponse{
		ProviderStatus:  providerResp.Status,
		Status:          mapInfinitePayStatus(providerResp.Status),
		PaidAmountCents: providerResp.PaidAmountCents,
		Currency:        providerResp.Currency,
		ReceiptURL:      providerResp.ReceiptURL,
		TransactionNSU:  providerResp.TransactionID,
	}, nil
}

func mapInfinitePayStatus(s string) PaymentStatus {
	switch s {
	case "approved", "captured":
		return PaymentStatusApproved
	case "rejected", "declined":
		return PaymentStatusRejected
	case "refunded":
		return PaymentStatusRefunded
	case "chargeback":
		return PaymentStatusChargeback
	case "pending_review", "under_review":
		return PaymentStatusPendingReview
	default:
		return PaymentStatusPending
	}
}
