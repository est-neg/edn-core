package payments

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func newTestHandler(orders *fakeOrderRepo) *Handler {
	statusSvc := NewOrderStatusService(orders, fakeSubscriptionRepo{}, fakeStatusCache{}, zap.NewNop())
	return NewHandler(nil, statusSvc, nil, nil, "Bearer test-admin-token", "", zap.NewNop())
}

// newRecoveryTestHandler creates a handler with both a CheckoutService (for recovery)
// and an OrderStatusService backed by the same order repo.
func newRecoveryTestHandler(orders *fakeOrderRepo, providerResp InfinitePayCheckoutResponse, providerErr error) *Handler {
	checkoutSvc := newResumeTestService(orders, providerResp, providerErr)
	statusSvc := NewOrderStatusService(orders, fakeSubscriptionRepo{}, fakeStatusCache{}, zap.NewNop())
	return NewHandler(checkoutSvc, statusSvc, nil, nil, "Bearer test-admin-token", "", zap.NewNop())
}

// seedRecoverableOrder seeds an order that is active/open but has no provider_checkout_url.
// PlanID and AmountCents match the fakeVersionedPlanRepo used by newResumeTestService so
// the recovery path can resolve the plan for the provider call.
func seedRecoverableOrder(orders *fakeOrderRepo, intentKey string) *checkout.Order {
	o := &checkout.Order{
		OrderNSU:            "nsu-" + intentKey[:8],
		PlanID:              "plan-basic", // must match fakeVersionedPlanRepo.PlanUUID
		PlanSlug:            "basic",
		BillingCycle:        "monthly",
		AmountCents:         9900, // must match plan.PriceCents in fake
		Currency:            "BRL",
		Status:              string(OrderStatusCreated),
		CheckoutIntentKey:   intentKey,
		ProviderCheckoutURL: "", // no URL — triggers recovery path
		InvoiceSlug:         "", // blank — recovery is deterministic
		ExpiresAt:           time.Now().UTC().Add(30 * time.Minute),
		CreatedAt:           time.Now().UTC().Add(-time.Hour),
		UpdatedAt:           time.Now().UTC().Add(-time.Minute),
	}
	orders.orders[o.OrderNSU] = o
	return o
}

func postJSON(t *testing.T, h http.HandlerFunc, path string, body interface{}, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func seedOrder(orders *fakeOrderRepo, intentKey, status, planSlug string, expiresAt time.Time, checkoutURL string) *checkout.Order {
	o := &checkout.Order{
		ID:                  primitive.NewObjectID(),
		OrderNSU:            "nsu-" + intentKey[:8],
		PlanSlug:            planSlug,
		BillingCycle:        "monthly",
		AmountCents:         9900,
		Currency:            "BRL",
		Status:              status,
		CheckoutIntentKey:   intentKey,
		ProviderCheckoutURL: checkoutURL,
		ExpiresAt:           expiresAt,
		CreatedAt:           time.Now().UTC().Add(-time.Hour),
		UpdatedAt:           time.Now().UTC().Add(-time.Minute),
	}
	orders.orders[o.OrderNSU] = o
	return o
}

// ─── TrackCheckoutSession tests ───────────────────────────────────────────────

// TestTrackCheckoutSession_ValidToken_Returns200 verifies the happy path:
// a valid intent key returns 200 with order progress and no customer PII.
func TestTrackCheckoutSession_ValidToken_Returns200(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	seedOrder(orders, intentKey, string(OrderStatusCheckoutCreated), "basic", expiresAt, "https://checkout.example/pay")

	h := newTestHandler(orders)
	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{"checkout_intent_key": intentKey}, "")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp TrackCheckoutResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CheckoutIntentKey != intentKey {
		t.Errorf("expected checkout_intent_key %q, got %q", intentKey, resp.CheckoutIntentKey)
	}
	if resp.Status != OrderStatusCheckoutCreated {
		t.Errorf("expected status %q, got %q", OrderStatusCheckoutCreated, resp.Status)
	}
	if resp.PlanSlug != "basic" {
		t.Errorf("expected plan_slug %q, got %q", "basic", resp.PlanSlug)
	}
	if !resp.Resumable {
		t.Error("expected resumable=true for open checkout")
	}
	if resp.CheckoutURL == "" {
		t.Error("expected checkout_url present when resumable and URL is set")
	}
	// Must not expose PII fields
	raw := rr.Body.String()
	if contains(raw, "customer") || contains(raw, "cpf") || contains(raw, "document") || contains(raw, "email") || contains(raw, "phone") {
		t.Errorf("response must not contain customer PII, got: %s", raw)
	}
}

// TestTrackCheckoutSession_BlankToken_Returns422 verifies that an empty
// checkout_intent_key is rejected before any storage lookup.
func TestTrackCheckoutSession_BlankToken_Returns422(t *testing.T) {
	orders := newFakeOrderRepo()
	h := newTestHandler(orders)

	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{"checkout_intent_key": "   "}, "")

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rr.Code)
	}
}

// TestTrackCheckoutSession_MissingField_Returns422 verifies that a body
// with no checkout_intent_key field returns 422.
func TestTrackCheckoutSession_MissingField_Returns422(t *testing.T) {
	orders := newFakeOrderRepo()
	h := newTestHandler(orders)

	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{}, "")

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rr.Code)
	}
}

// TestTrackCheckoutSession_UnknownToken_Returns404 verifies that an unknown
// intent key returns 404 without leaking internal state details.
func TestTrackCheckoutSession_UnknownToken_Returns404(t *testing.T) {
	orders := newFakeOrderRepo()
	h := newTestHandler(orders)

	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{"checkout_intent_key": "no-such-key"}, "")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	// Response must not expose internal error details
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected error field in 404 response")
	}
}

// TestTrackCheckoutSession_PaidOrder_NotResumable_NoCheckoutURL verifies that
// a paid order is correctly reflected as non-resumable with no checkout_url.
func TestTrackCheckoutSession_PaidOrder_NotResumable_NoCheckoutURL(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	o := seedOrder(orders, intentKey, string(OrderStatusPaid), "pro", time.Time{}, "https://checkout.example/pay")
	o.ReceiptURL = "https://receipt.example/r123"

	h := newTestHandler(orders)
	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{"checkout_intent_key": intentKey}, "")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp TrackCheckoutResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Resumable {
		t.Error("paid order must not be resumable")
	}
	if resp.CheckoutURL != "" {
		t.Error("checkout_url must be absent for paid order")
	}
	if resp.ReceiptURL == "" {
		t.Error("receipt_url must be present for paid order")
	}
}

// ─── SearchOrdersByDocument tests ────────────────────────────────────────────

// TestSearchOrdersByDocument_Unauthorized_Returns401 verifies that a missing or
// wrong Authorization header results in 401.
func TestSearchOrdersByDocument_Unauthorized_Returns401(t *testing.T) {
	orders := newFakeOrderRepo()
	h := newTestHandler(orders)

	cases := []struct {
		name   string
		header string
	}{
		{"missing auth", ""},
		{"wrong token", "Bearer wrong-token"},
		{"partial token", "test-admin-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := postJSON(t, h.SearchOrdersByDocument, "/api/v1/orders/search",
				map[string]string{"document": "529.982.247-25"}, tc.header)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("expected 401, got %d", rr.Code)
			}
		})
	}
}

// TestSearchOrdersByDocument_AuthorizedFormattedCPF_Returns200 verifies that
// a correctly authorized request with a formatted CPF is normalized server-side
// and returns 200 with the matching orders.
func TestSearchOrdersByDocument_AuthorizedFormattedCPF_Returns200(t *testing.T) {
	orders := newFakeOrderRepo()
	// Inject an order with the normalized CPF "52998224725"
	nsu := "order-search-001"
	orders.orders[nsu] = &checkout.Order{
		OrderNSU:          nsu,
		CheckoutIntentKey: "test-intent-key-shouldnotleak",
		PlanSlug:          "basic",
		BillingCycle:      "monthly",
		AmountCents:       9900,
		Currency:          "BRL",
		Status:            string(OrderStatusPaid),
		CustomerDocument:  "52998224725", // normalized
		CreatedAt:         time.Now().UTC().Add(-24 * time.Hour),
		UpdatedAt:         time.Now().UTC(),
	}

	h := newTestHandler(orders)
	// Send the CPF with formatting — server must normalize
	rr := postJSON(t, h.SearchOrdersByDocument, "/api/v1/orders/search",
		map[string]string{"document": "529.982.247-25"}, "Bearer test-admin-token")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp AdminOrderSearchResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(resp.Orders))
	}
	if resp.Orders[0].OrderNSU != nsu {
		t.Errorf("expected order_nsu %q, got %q", nsu, resp.Orders[0].OrderNSU)
	}
	// Response must not contain raw CPF or capability token
	raw := rr.Body.String()
	if contains(raw, "52998224725") || contains(raw, "529.982.247-25") {
		t.Errorf("response must not contain CPF, got: %s", raw)
	}
	if contains(raw, "test-intent-key-shouldnotleak") {
		t.Errorf("admin response must not expose checkout_intent_key, got: %s", raw)
	}
}

// TestSearchOrdersByDocument_InvalidCPF_Returns422 verifies that an invalid CPF
// is rejected before any storage lookup, with a 422.
func TestSearchOrdersByDocument_InvalidCPF_Returns422(t *testing.T) {
	orders := newFakeOrderRepo()
	h := newTestHandler(orders)

	cases := []string{
		"000.000.000-00", // all-zeros invalid
		"123.456.789-00", // bad check digits
		"",               // empty (after blank check)
		"not-a-cpf",      // garbage
	}
	for _, doc := range cases {
		body := map[string]string{"document": doc}
		rr := postJSON(t, h.SearchOrdersByDocument, "/api/v1/orders/search",
			body, "Bearer test-admin-token")
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("doc %q: expected 422, got %d", doc, rr.Code)
		}
	}
}

// TestSearchOrdersByDocument_NoOrders_Returns200EmptyList verifies that an
// authorized search with a valid CPF that has no orders returns 200 with an
// empty list (not 404 or 500).
func TestSearchOrdersByDocument_NoOrders_Returns200EmptyList(t *testing.T) {
	orders := newFakeOrderRepo()
	h := newTestHandler(orders)

	rr := postJSON(t, h.SearchOrdersByDocument, "/api/v1/orders/search",
		map[string]string{"document": "529.982.247-25"}, "Bearer test-admin-token")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp AdminOrderSearchResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Orders == nil {
		t.Error("orders field must not be null")
	}
	if len(resp.Orders) != 0 {
		t.Errorf("expected empty orders, got %d", len(resp.Orders))
	}
}

// TestTrackCheckoutSession_RecoversMissingURL_Returns200 verifies that when an active
// order has no provider_checkout_url, the tracking endpoint recovers synchronously and
// returns 200 with checkout_url and resumable=true.
func TestTrackCheckoutSession_RecoversMissingURL_Returns200(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	seedRecoverableOrder(orders, intentKey)

	h := newRecoveryTestHandler(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/recovered",
		InvoiceSlug: "inv-recovered",
	}, nil)

	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{"checkout_intent_key": intentKey}, "")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp TrackCheckoutResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CheckoutURL == "" {
		t.Error("expected checkout_url present after recovery")
	}
	if resp.CheckoutURL != "https://checkout.example/recovered" {
		t.Errorf("expected recovered checkout_url, got %q", resp.CheckoutURL)
	}
	if !resp.Resumable {
		t.Error("expected resumable=true after successful recovery")
	}
	if resp.CheckoutIntentKey != intentKey {
		t.Errorf("expected checkout_intent_key %q, got %q", intentKey, resp.CheckoutIntentKey)
	}
}

// TestTrackCheckoutSession_RecoveryAmbiguous_Returns503 verifies that when an active
// order has no provider_checkout_url but has an invoice_slug (partial provider state),
// the tracking endpoint returns 503 with the provider_state_ambiguous error code.
func TestTrackCheckoutSession_RecoveryAmbiguous_Returns503(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	o := seedRecoverableOrder(orders, intentKey)
	o.InvoiceSlug = "inv-partial" // invoice_slug present without URL = ambiguous
	orders.orders[o.OrderNSU] = o

	h := newRecoveryTestHandler(orders, InfinitePayCheckoutResponse{}, nil)

	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{"checkout_intent_key": intentKey}, "")

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeProviderStateAmbiguous {
		t.Errorf("expected error_code %q, got %q", ErrCodeProviderStateAmbiguous, body.ErrorCode)
	}
}

// TestTrackCheckoutSession_NonTerminalExpired_ReflectsExpiredStatus verifies that
// when an order is non-terminal but its expires_at has passed, the tracking response
// reflects status=expired and resumable=false with no checkout_url.
func TestTrackCheckoutSession_NonTerminalExpired_ReflectsExpiredStatus(t *testing.T) {
	orders := newFakeOrderRepo()
	intentKey := GenerateCheckoutIntentKey()
	// expires_at in the past, stored status still "checkout_created"
	seedOrder(orders, intentKey, string(OrderStatusCheckoutCreated), "basic",
		time.Now().UTC().Add(-5*time.Minute), "https://checkout.example/pay")

	h := newTestHandler(orders)
	rr := postJSON(t, h.TrackCheckoutSession, "/v1/checkout/sessions/track",
		map[string]string{"checkout_intent_key": intentKey}, "")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp TrackCheckoutResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != OrderStatusExpired {
		t.Errorf("expected status=%q, got %q", OrderStatusExpired, resp.Status)
	}
	if resp.Resumable {
		t.Error("expired order must not be resumable")
	}
	if resp.CheckoutURL != "" {
		t.Error("checkout_url must be absent for expired order")
	}
}

// contains is a helper for case-sensitive substring checks in response bodies.
func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
