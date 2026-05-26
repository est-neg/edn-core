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

// fakeWebhookRunner is a test double for webhookRunner that returns a preset error.
type fakeWebhookRunner struct {
	err error
}

func (f *fakeWebhookRunner) Handle(_ context.Context, _ []byte) error {
	return f.err
}

// newReconcileHandler builds a Handler wired to a fakeWebhookRunner.
// internalToken is the optional secondary token checked by HandleWebhookReconcile.
func newReconcileHandler(webhookErr error, internalToken string) *Handler {
	return &Handler{
		webhook:       &fakeWebhookRunner{err: webhookErr},
		internalToken: internalToken,
		log:           zap.NewNop(),
	}
}

// postReconcile fires a POST to the reconcile route against the given handler.
func postReconcile(t *testing.T, h *Handler, body []byte, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/payments/providers/infinitepay/webhook-reconcile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-EDN-Internal-Token", token)
	}
	rr := httptest.NewRecorder()
	h.HandleWebhookReconcile(rr, req)
	return rr
}

func validReconcileBody() []byte {
	return []byte(`{"transaction_id":"tx-001","order_id":"order-001","invoice_id":"inv-001"}`)
}

// ─── Success ──────────────────────────────────────────────────────────────────

func TestHandleWebhookReconcile_Success_Returns200(t *testing.T) {
	h := newReconcileHandler(nil, "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

// ─── 422 deterministic integrity failures ─────────────────────────────────────

func TestHandleWebhookReconcile_TransactionMismatch_Returns422(t *testing.T) {
	h := newReconcileHandler(ErrTransactionMismatch, "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ErrTransactionMismatch: expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleWebhookReconcile_AmountMismatch_Returns422(t *testing.T) {
	h := newReconcileHandler(ErrAmountMismatch, "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ErrAmountMismatch: expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleWebhookReconcile_OrderNotFound_Returns422(t *testing.T) {
	h := newReconcileHandler(ErrOrderNotFound, "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ErrOrderNotFound: expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleWebhookReconcile_InvalidRequest_Returns422(t *testing.T) {
	h := newReconcileHandler(ErrInvalidRequest, "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ErrInvalidRequest: expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ─── 503 temporary (lock conflict) ───────────────────────────────────────────

func TestHandleWebhookReconcile_LockConflict_Returns503(t *testing.T) {
	h := newReconcileHandler(ErrLockConflict, "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("ErrLockConflict: expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ─── 500 unexpected errors ────────────────────────────────────────────────────

func TestHandleWebhookReconcile_UnexpectedError_Returns500(t *testing.T) {
	h := newReconcileHandler(errors.New("database timeout"), "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected error: expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ─── Internal token gate ──────────────────────────────────────────────────────

func TestHandleWebhookReconcile_WrongToken_Returns401(t *testing.T) {
	h := newReconcileHandler(nil, "correct-token")
	rr := postReconcile(t, h, validReconcileBody(), "wrong-token")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleWebhookReconcile_MissingToken_Returns401(t *testing.T) {
	h := newReconcileHandler(nil, "correct-token")
	rr := postReconcile(t, h, validReconcileBody(), "") // no token header
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleWebhookReconcile_CorrectToken_Returns200(t *testing.T) {
	h := newReconcileHandler(nil, "correct-token")
	rr := postReconcile(t, h, validReconcileBody(), "correct-token")
	if rr.Code != http.StatusOK {
		t.Fatalf("correct token: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleWebhookReconcile_NoTokenRequired_Passes(t *testing.T) {
	// When internalToken is empty, no secondary token check is enforced.
	h := newReconcileHandler(nil, "")
	rr := postReconcile(t, h, validReconcileBody(), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("no token required: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ─── Input validation ─────────────────────────────────────────────────────────

func TestHandleWebhookReconcile_WrongContentType_Returns415(t *testing.T) {
	h := newReconcileHandler(nil, "")
	req := httptest.NewRequest(http.MethodPost, "/internal/payments/providers/infinitepay/webhook-reconcile", bytes.NewReader(validReconcileBody()))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	h.HandleWebhookReconcile(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415 for text/plain, got %d", rr.Code)
	}
}

func TestHandleWebhookReconcile_EmptyBody_Returns400(t *testing.T) {
	h := newReconcileHandler(nil, "")
	req := httptest.NewRequest(http.MethodPost, "/internal/payments/providers/infinitepay/webhook-reconcile", bytes.NewReader([]byte{}))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleWebhookReconcile(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty body, got %d", rr.Code)
	}
}
