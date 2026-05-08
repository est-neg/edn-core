package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

// newCheckoutHandlerWithService builds a Handler wired to the given CheckoutService
// for CreateCheckoutSession handler tests.
func newCheckoutHandlerWithService(svc *CheckoutService) *Handler {
	return NewHandler(svc, nil, nil, nil, "Bearer test-admin-token", zap.NewNop())
}

func postCheckout(t *testing.T, h *Handler, body CreateCheckoutRequest, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/checkout/sessions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rr := httptest.NewRecorder()
	h.CreateCheckoutSession(rr, req)
	return rr
}

// ─── CreateCheckoutSession tests ─────────────────────────────────────────────

// TestCreateCheckoutSession_Created_Returns201 verifies that a fresh checkout
// returns 201 with checkout_intent_key and checkout_url.
func TestCreateCheckoutSession_Created_Returns201(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/new",
		InvoiceSlug: "inv-new",
	}, nil)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-key-create-001")

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp CreateCheckoutResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.CheckoutIntentKey == "" {
		t.Error("expected non-empty checkout_intent_key in 201 response")
	}
	if resp.CheckoutURL == "" {
		t.Error("expected non-empty checkout_url in 201 response")
	}
	if resp.OrderNSU == "" {
		t.Error("expected non-empty order_nsu in 201 response")
	}
}

// TestCreateCheckoutSession_Resumed_Returns200 verifies that supplying a
// checkout_intent_key from a prior 201 returns 200 (create-or-resume).
func TestCreateCheckoutSession_Resumed_Returns200(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/resume",
		InvoiceSlug: "inv-resume",
	}, nil)
	h := newCheckoutHandlerWithService(svc)

	// First call — creates the checkout.
	first := postCheckout(t, h, validCheckoutRequest(), "idem-key-resume-first")
	if first.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", first.Code, first.Body.String())
	}
	var created CreateCheckoutResponse
	if err := json.NewDecoder(first.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	// Second call — resume using intent key.
	resumeReq := validCheckoutRequest()
	resumeReq.CheckoutIntentKey = created.CheckoutIntentKey

	second := postCheckout(t, h, resumeReq, "idem-key-resume-second")
	if second.Code != http.StatusOK {
		t.Fatalf("resume: expected 200, got %d: %s", second.Code, second.Body.String())
	}
	var resumed CreateCheckoutResponse
	if err := json.NewDecoder(second.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode resume: %v", err)
	}
	if resumed.OrderNSU != created.OrderNSU {
		t.Errorf("expected same order_nsu %q, got %q", created.OrderNSU, resumed.OrderNSU)
	}
	if resumed.CheckoutIntentKey != created.CheckoutIntentKey {
		t.Errorf("expected same checkout_intent_key %q, got %q", created.CheckoutIntentKey, resumed.CheckoutIntentKey)
	}
}

// TestCreateCheckoutSession_IdempotencyConflict_Returns409 verifies that a
// concurrent duplicate idempotency-key attempt returns 409 with
// error_code=checkout_idempotency_conflict.
func TestCreateCheckoutSession_IdempotencyConflict_Returns409(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, ErrCheckoutConflict)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-conflict-key")
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeCheckoutIdempotencyConflict {
		t.Errorf("expected error_code %q, got %q", ErrCodeCheckoutIdempotencyConflict, body.ErrorCode)
	}
}

// TestCreateCheckoutSession_InProgress_Returns409 verifies that a checkout
// already in-flight returns 409 with error_code=checkout_in_progress.
func TestCreateCheckoutSession_InProgress_Returns409(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, ErrCheckoutInProgress)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-inprogress-key")
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeCheckoutInProgress {
		t.Errorf("expected error_code %q, got %q", ErrCodeCheckoutInProgress, body.ErrorCode)
	}
}

// TestCreateCheckoutSession_AlreadyOpen_Returns409 verifies that when an active open
// order exists for the same fingerprint, the handler returns 409 with
// error_code=checkout_already_open and leaks no session data.
func TestCreateCheckoutSession_AlreadyOpen_Returns409(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, ErrCheckoutAlreadyOpen)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-already-open-key")
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeCheckoutAlreadyOpen {
		t.Errorf("expected error_code %q, got %q", ErrCodeCheckoutAlreadyOpen, body.ErrorCode)
	}
	if body.CheckoutIntentKey != "" {
		t.Errorf("expected empty checkout_intent_key in already-open response, got %q", body.CheckoutIntentKey)
	}
	if body.OrderNSU != "" {
		t.Errorf("expected empty order_nsu in already-open response, got %q", body.OrderNSU)
	}
}

// TestCreateCheckoutSession_ProviderAmbiguous_Returns503 verifies that a
// provider-state-ambiguous error returns 503 with the correct error_code.
func TestCreateCheckoutSession_ProviderAmbiguous_Returns503(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, ErrProviderStateAmbiguous)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-ambiguous-key")
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

func TestCreateCheckoutSession_ProviderAmbiguous_IncludesResumeContextWhenAvailable(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.updateProviderURLErr = errors.New("write concern timeout")
	orders.updateStatusErr = errors.New("write concern timeout")
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/ambiguous",
		InvoiceSlug: "inv-ambiguous",
	}, nil)
	svc.idempotency.(*fakeCheckoutIdempotencyRepo).commitErr = errors.New("mongo write timeout")
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-ambiguous-context-key")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeProviderStateAmbiguous {
		t.Fatalf("expected error_code %q, got %q", ErrCodeProviderStateAmbiguous, body.ErrorCode)
	}
	if body.OrderNSU == "" {
		t.Fatal("expected non-empty order_nsu in ambiguous 503 response")
	}
	if body.CheckoutIntentKey == "" {
		t.Fatal("expected non-empty checkout_intent_key in ambiguous 503 response")
	}
	order, err := orders.GetByNSU(context.Background(), body.OrderNSU)
	if err != nil {
		t.Fatalf("GetByNSU returned error: %v", err)
	}
	if order.CheckoutIntentKey != body.CheckoutIntentKey {
		t.Fatalf("expected checkout_intent_key %q, got %q", order.CheckoutIntentKey, body.CheckoutIntentKey)
	}
}

// TestCreateCheckoutSession_NonResumable_Returns409 verifies that a
// checkout_non_resumable state returns a distinct 409 error_code.
func TestCreateCheckoutSession_NonResumable_Returns409(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, ErrCheckoutNonResumable)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-nonresumable-key")
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeCheckoutNonResumable {
		t.Errorf("expected error_code %q, got %q", ErrCodeCheckoutNonResumable, body.ErrorCode)
	}
}

// TestCreateCheckoutSession_RecoveryRequired_Returns503 verifies that a
// checkout_recovery_required error returns 503 with the correct error_code.
func TestCreateCheckoutSession_RecoveryRequired_Returns503(t *testing.T) {
	orders := newFakeOrderRepo()
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{}, ErrCheckoutRecoveryRequired)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-recovery-required-key")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeCheckoutRecoveryRequired {
		t.Errorf("expected error_code %q, got %q", ErrCodeCheckoutRecoveryRequired, body.ErrorCode)
	}
}

func TestCreateCheckoutSession_RecoveryRequired_IncludesResumeContextWhenAvailable(t *testing.T) {
	orders := newFakeOrderRepo()
	orders.updateProviderURLErr = errors.New("write concern timeout")
	orders.updateStatusErr = errors.New("write concern timeout")
	svc := newResumeTestService(orders, InfinitePayCheckoutResponse{
		CheckoutURL: "https://checkout.example/recover",
		InvoiceSlug: "inv-recover",
	}, nil)
	h := newCheckoutHandlerWithService(svc)

	rr := postCheckout(t, h, validCheckoutRequest(), "idem-recovery-context-key")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	var body CheckoutErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorCode != ErrCodeCheckoutRecoveryRequired {
		t.Fatalf("expected error_code %q, got %q", ErrCodeCheckoutRecoveryRequired, body.ErrorCode)
	}
	if body.OrderNSU == "" {
		t.Fatal("expected non-empty order_nsu in recovery-required 503 response")
	}
	if body.CheckoutIntentKey == "" {
		t.Fatal("expected non-empty checkout_intent_key in recovery-required 503 response")
	}
	order, err := orders.GetByNSU(context.Background(), body.OrderNSU)
	if err != nil {
		t.Fatalf("GetByNSU returned error: %v", err)
	}
	if order.CheckoutIntentKey != body.CheckoutIntentKey {
		t.Fatalf("expected checkout_intent_key %q, got %q", order.CheckoutIntentKey, body.CheckoutIntentKey)
	}
}
