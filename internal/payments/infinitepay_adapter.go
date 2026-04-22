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

// infinitePayCreateCheckoutPayload is the wire format for InfinitePay checkout creation.
// Field names match InfinitePay's documented API schema.
type infinitePayCreateCheckoutPayload struct {
	OrderID     string              `json:"order_id"`
	WebhookURL  string              `json:"webhook_url"`
	RedirectURL string              `json:"redirect_url"`
	Customer    infinitePayCustomer `json:"customer"`
	Items       []infinitePayItem   `json:"items"`
}

type infinitePayCustomer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
}

type infinitePayItem struct {
	Name        string `json:"name"`
	Quantity    int    `json:"quantity"`
	AmountCents int64  `json:"amount_cents"`
}

type infinitePayCreateCheckoutResponse struct {
	CheckoutURL string `json:"checkout_url"`
	InvoiceID   string `json:"invoice_id"`
}

type infinitePayVerifyResponse struct {
	Status          string `json:"status"`
	PaidAmountCents int64  `json:"paid_amount_cents"`
	Currency        string `json:"currency"`
	ReceiptURL      string `json:"receipt_url"`
	TransactionID   string `json:"transaction_id"`
}

func (a *infinitePayAdapter) CreateCheckout(ctx context.Context, req InfinitePayCheckoutRequest) (InfinitePayCheckoutResponse, error) {
	payload := infinitePayCreateCheckoutPayload{
		OrderID:     req.OrderNSU,
		WebhookURL:  req.WebhookURL,
		RedirectURL: req.RedirectURL,
		Customer: infinitePayCustomer{
			Name:  req.CustomerName,
			Email: req.CustomerEmail,
			Phone: req.CustomerPhone,
		},
		Items: []infinitePayItem{
			{
				Name:        req.PlanName,
				Quantity:    1,
				AmountCents: req.AmountCents,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("marshal checkout payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/checkout", bytes.NewReader(body))
	if err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("build checkout request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiToken)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("provider checkout http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Log status code only — not response body (may contain sensitive data)
		a.log.Warn("provider checkout non-2xx", zap.Int("status", resp.StatusCode))
		return InfinitePayCheckoutResponse{}, fmt.Errorf("provider checkout status %d", resp.StatusCode)
	}

	var providerResp infinitePayCreateCheckoutResponse
	if err := json.Unmarshal(respBody, &providerResp); err != nil {
		return InfinitePayCheckoutResponse{}, fmt.Errorf("decode checkout response: %w", err)
	}

	return InfinitePayCheckoutResponse{
		CheckoutURL: providerResp.CheckoutURL,
		InvoiceSlug: providerResp.InvoiceID,
	}, nil
}

func (a *infinitePayAdapter) VerifyPayment(ctx context.Context, invoiceSlug, orderNSU, transactionNSU string) (InfinitePayVerifyResponse, error) {
	url := fmt.Sprintf("%s/v1/payments/%s", a.baseURL, invoiceSlug)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return InfinitePayVerifyResponse{}, fmt.Errorf("build verify request: %w", err)
	}
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

	var providerResp infinitePayVerifyResponse
	if err := json.Unmarshal(respBody, &providerResp); err != nil {
		return InfinitePayVerifyResponse{}, fmt.Errorf("decode verify response: %w", err)
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
