package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func newTestAdapter(t *testing.T, baseURL string) InfinitePayClient {
	t.Helper()
	log, _ := zap.NewDevelopment()
	return NewInfinitePayAdapter(baseURL, "test-token", 5, log)
}

// TestCreateCheckout_usesLinksEndpoint verifies that CreateCheckout sends a POST
// to /links with the correct Authorization header and payload fields.
func TestCreateCheckout_usesLinksEndpoint(t *testing.T) {
	var capturedMethod string
	var capturedPath string
	var capturedAuth string
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")

		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &capturedBody) //nolint:errcheck

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"checkout_url": "https://checkout.infinitepay.io/pay/abc123",
			"invoice_id":   "inv-001",
		})
	}))
	defer srv.Close()

	adapter := newTestAdapter(t, srv.URL)
	req := InfinitePayCheckoutRequest{
		Handle:        "edn-villenneve",
		OrderNSU:      "order-001",
		PlanName:      "Plano Mensal",
		AmountCents:   100,
		CustomerName:  "Maria Silva",
		CustomerEmail: "maria@example.com",
		CustomerPhone: "+5511999999999",
		WebhookURL:    "https://example.com/webhook",
		RedirectURL:   "https://example.com/return",
	}

	resp, err := adapter.CreateCheckout(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateCheckout error: %v", err)
	}

	// Verify HTTP contract
	if capturedMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", capturedMethod)
	}
	if capturedPath != "/links" {
		t.Errorf("path: got %q, want /links", capturedPath)
	}
	// /links must NOT carry Authorization — the handle in the payload identifies the merchant.
	if capturedAuth != "" {
		t.Errorf("auth header: /links must not receive Authorization, got %q", capturedAuth)
	}

	// Verify payload fields
	if capturedBody["handle"] != "edn-villenneve" {
		t.Errorf("handle: got %v", capturedBody["handle"])
	}
	if capturedBody["order_id"] != "order-001" {
		t.Errorf("order_id: got %v, want order-001", capturedBody["order_id"])
	}
	if capturedBody["redirect_url"] != "https://example.com/return" {
		t.Errorf("redirect_url: got %v", capturedBody["redirect_url"])
	}
	if capturedBody["webhook_url"] != "https://example.com/webhook" {
		t.Errorf("webhook_url: got %v", capturedBody["webhook_url"])
	}

	customer, ok := capturedBody["customer"].(map[string]interface{})
	if !ok {
		t.Fatal("customer field missing or wrong type")
	}
	if customer["phone_number"] != "+5511999999999" {
		t.Errorf("customer.phone_number: got %v", customer["phone_number"])
	}
	if customer["name"] != "Maria Silva" {
		t.Errorf("customer.name: got %v", customer["name"])
	}
	if customer["email"] != "maria@example.com" {
		t.Errorf("customer.email: got %v", customer["email"])
	}
	// document must NOT be sent
	if _, exists := customer["document"]; exists {
		t.Error("customer.document must not be sent to provider")
	}

	items, ok := capturedBody["items"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatal("items field missing or wrong length")
	}
	item := items[0].(map[string]interface{})
	if item["quantity"] != float64(1) {
		t.Errorf("items[0].quantity: got %v", item["quantity"])
	}
	if item["price"] != float64(100) {
		t.Errorf("items[0].price: got %v", item["price"])
	}
	if item["description"] != "Plano Mensal" {
		t.Errorf("items[0].description: got %v", item["description"])
	}

	// Verify parsed response
	if resp.CheckoutURL != "https://checkout.infinitepay.io/pay/abc123" {
		t.Errorf("CheckoutURL: got %q", resp.CheckoutURL)
	}
	if resp.InvoiceSlug != "inv-001" {
		t.Errorf("InvoiceSlug: got %q", resp.InvoiceSlug)
	}
}

// TestCreateCheckout_tolerantResponseParsing verifies that the adapter resolves
// checkout URL from "link" or "url" when "checkout_url" is absent, and "id"
// when "invoice_id" is absent.
func TestCreateCheckout_tolerantResponseParsing(t *testing.T) {
	cases := []struct {
		name        string
		respPayload map[string]string
		wantURL     string
		wantInvoice string
	}{
		{
			name:        "checkout_url and invoice_id",
			respPayload: map[string]string{"checkout_url": "https://url1", "invoice_id": "inv-a"},
			wantURL:     "https://url1",
			wantInvoice: "inv-a",
		},
		{
			name:        "link fallback",
			respPayload: map[string]string{"link": "https://url2", "invoice_id": "inv-b"},
			wantURL:     "https://url2",
			wantInvoice: "inv-b",
		},
		{
			name:        "url fallback",
			respPayload: map[string]string{"url": "https://url3", "id": "inv-c"},
			wantURL:     "https://url3",
			wantInvoice: "inv-c",
		},
		{
			name:        "id fallback when invoice_id absent",
			respPayload: map[string]string{"checkout_url": "https://url4", "id": "inv-d"},
			wantURL:     "https://url4",
			wantInvoice: "inv-d",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tc.respPayload) //nolint:errcheck
			}))
			defer srv.Close()

			adapter := newTestAdapter(t, srv.URL)
			resp, err := adapter.CreateCheckout(context.Background(), InfinitePayCheckoutRequest{
				Handle:      "handle",
				PlanName:    "Plan",
				AmountCents: 100,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.CheckoutURL != tc.wantURL {
				t.Errorf("CheckoutURL: got %q, want %q", resp.CheckoutURL, tc.wantURL)
			}
			if resp.InvoiceSlug != tc.wantInvoice {
				t.Errorf("InvoiceSlug: got %q, want %q", resp.InvoiceSlug, tc.wantInvoice)
			}
		})
	}
}

// TestVerifyPayment_usesPaymentCheckEndpoint verifies that VerifyPayment sends a POST
// to /payment_check with correct Authorization header and identifier fields.
func TestVerifyPayment_usesPaymentCheckEndpoint(t *testing.T) {
	var capturedMethod string
	var capturedPath string
	var capturedAuth string
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")

		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &capturedBody) //nolint:errcheck

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
			"status":            "approved",
			"transaction_id":    "txn-999",
			"paid_amount_cents": 100,
			"currency":          "BRL",
			"receipt_url":       "https://receipt.example.com",
		})
	}))
	defer srv.Close()

	adapter := newTestAdapter(t, srv.URL)
	resp, err := adapter.VerifyPayment(context.Background(), "inv-001", "order-001", "txn-001")
	if err != nil {
		t.Fatalf("VerifyPayment error: %v", err)
	}

	// Verify HTTP contract
	if capturedMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", capturedMethod)
	}
	if capturedPath != "/payment_check" {
		t.Errorf("path: got %q, want /payment_check", capturedPath)
	}
	if capturedAuth != "Bearer test-token" {
		t.Errorf("auth header: got %q, want Bearer test-token", capturedAuth)
	}

	// Verify payload serializes the three identifiers
	if capturedBody["invoice_id"] != "inv-001" {
		t.Errorf("invoice_id: got %v", capturedBody["invoice_id"])
	}
	if capturedBody["order_id"] != "order-001" {
		t.Errorf("order_id: got %v", capturedBody["order_id"])
	}
	if capturedBody["transaction_id"] != "txn-001" {
		t.Errorf("transaction_id: got %v", capturedBody["transaction_id"])
	}

	// Verify parsed response
	if resp.ProviderStatus != "approved" {
		t.Errorf("ProviderStatus: got %q", resp.ProviderStatus)
	}
	if resp.Status != PaymentStatusApproved {
		t.Errorf("Status: got %q", resp.Status)
	}
	if resp.TransactionNSU != "txn-999" {
		t.Errorf("TransactionNSU: got %q", resp.TransactionNSU)
	}
	if resp.PaidAmountCents != 100 {
		t.Errorf("PaidAmountCents: got %d", resp.PaidAmountCents)
	}
	if resp.ReceiptURL != "https://receipt.example.com" {
		t.Errorf("ReceiptURL: got %q", resp.ReceiptURL)
	}
}

// TestVerifyPayment_omitsEmptyIdentifiers verifies that empty strings are omitted
// from the payment_check payload (omitempty).
func TestVerifyPayment_omitsEmptyIdentifiers(t *testing.T) {
	var capturedBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &capturedBody) //nolint:errcheck

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status":         "pending",
			"transaction_id": "",
		})
	}))
	defer srv.Close()

	adapter := newTestAdapter(t, srv.URL)
	// Only invoice_id provided; orderNSU and transactionNSU are empty
	_, err := adapter.VerifyPayment(context.Background(), "inv-only", "", "")
	if err != nil {
		t.Fatalf("VerifyPayment error: %v", err)
	}

	if _, exists := capturedBody["order_id"]; exists {
		t.Error("order_id must be omitted when empty")
	}
	if _, exists := capturedBody["transaction_id"]; exists {
		t.Error("transaction_id must be omitted when empty")
	}
	if capturedBody["invoice_id"] != "inv-only" {
		t.Errorf("invoice_id: got %v", capturedBody["invoice_id"])
	}
}

// TestCreateCheckout_2xxMissingURLReturnsError verifies that a 2xx response without
// any recognisable checkout URL field is treated as a provider error.
func TestCreateCheckout_2xxMissingURLReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 200 OK but no URL field in any variant
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"invoice_id": "inv-missing-url",
		})
	}))
	defer srv.Close()

	adapter := newTestAdapter(t, srv.URL)
	_, err := adapter.CreateCheckout(context.Background(), InfinitePayCheckoutRequest{Handle: "h", PlanName: "P", AmountCents: 100})
	if err == nil {
		t.Fatal("expected error when checkout URL is absent in 2xx response")
	}
	if err.Error() != "provider checkout missing checkout url" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestCreateCheckout_non2xxReturnsError verifies provider error responses are surfaced correctly.
func TestCreateCheckout_non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	adapter := newTestAdapter(t, srv.URL)
	_, err := adapter.CreateCheckout(context.Background(), InfinitePayCheckoutRequest{Handle: "h"})
	if err == nil {
		t.Error("expected error for non-2xx response")
	}
}

// TestCreateCheckout_deterministic4xx_returnsProviderCreateRejectedError verifies that
// deterministic 4xx responses from /links return a typed *ProviderCreateRejectedError
// carrying the exact status code.
func TestCreateCheckout_deterministic4xx_returnsProviderCreateRejectedError(t *testing.T) {
	deterministicCodes := []int{400, 401, 402, 403, 404, 422}
	for _, code := range deterministicCodes {
		code := code
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			adapter := newTestAdapter(t, srv.URL)
			_, err := adapter.CreateCheckout(context.Background(), InfinitePayCheckoutRequest{Handle: "h"})
			if err == nil {
				t.Fatalf("status %d: expected error", code)
			}
			var rejErr *ProviderCreateRejectedError
			if !errors.As(err, &rejErr) {
				t.Fatalf("status %d: expected *ProviderCreateRejectedError, got %T: %v", code, err, err)
			}
			if rejErr.StatusCode != code {
				t.Errorf("status %d: StatusCode field: got %d", code, rejErr.StatusCode)
			}
		})
	}
}

// TestCreateCheckout_transientErrors_doNotReturnProviderCreateRejectedError verifies that
// 408, 429, 5xx, and transport errors do NOT return *ProviderCreateRejectedError.
func TestCreateCheckout_transientErrors_doNotReturnProviderCreateRejectedError(t *testing.T) {
	transientCodes := []int{408, 429, 500, 502, 503}
	for _, code := range transientCodes {
		code := code
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			adapter := newTestAdapter(t, srv.URL)
			_, err := adapter.CreateCheckout(context.Background(), InfinitePayCheckoutRequest{Handle: "h"})
			if err == nil {
				t.Fatalf("status %d: expected error", code)
			}
			var rejErr *ProviderCreateRejectedError
			if errors.As(err, &rejErr) {
				t.Errorf("status %d: must not return *ProviderCreateRejectedError for transient code", code)
			}
		})
	}
}

// TestVerifyPayment_non2xxReturnsError verifies provider error responses are surfaced correctly.
func TestVerifyPayment_non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	adapter := newTestAdapter(t, srv.URL)
	_, err := adapter.VerifyPayment(context.Background(), "inv", "ord", "txn")
	if err == nil {
		t.Error("expected error for non-2xx response")
	}
}
