package leads_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/leads"
	"github.com/villenneve/vil-core/internal/platform/config"
)

// --- fakes ---

type fakeSubmitter struct {
	err error
}

func (f *fakeSubmitter) Submit(_ context.Context, _ leads.SubmitRequest) error {
	return f.err
}

// --- helpers ---

func newHandler(t *testing.T, submitter leads.Submitter, token string) *leads.Handler {
	t.Helper()
	cfg := config.LeadsConfig{
		AuthHeader:     "Authorization",
		AuthToken:      token,
		MaxBodyBytes:   16384,
		DedupWindowSec: 300,
	}
	return leads.NewHandler(submitter, cfg, zap.NewNop())
}

func validBody() map[string]any {
	return map[string]any{
		"source":      "lumina-ia-site",
		"submittedAt": time.Now().UTC().Format(time.RFC3339),
		"lead": map[string]any{
			"name":         "João Silva",
			"businessName": "Clínica Saúde+",
			"whatsapp":     "11999990000",
			"profile":      "medical",
			"consent":      true,
		},
	}
}

func postLeads(t *testing.T, handler http.Handler, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/leads", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body
}

// --- tests ---

func TestHandler_MissingToken_Returns401(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	req := httptest.NewRequest(http.MethodPost, "/api/leads", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	body := decodeError(t, rec)
	if body["error"] != "unauthorized" {
		t.Errorf("expected error=unauthorized, got %q", body["error"])
	}
}

func TestHandler_InvalidToken_Returns401(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	rec := postLeads(t, h, validBody(), "Bearer wrong-token")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandler_WrongContentType_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	req := httptest.NewRequest(http.MethodPost, "/api/leads", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_InvalidSource_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := validBody()
	body["source"] = "unknown-source"
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

func TestHandler_MissingConsentField_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := validBody()
	lead := body["lead"].(map[string]any)
	lead["consent"] = false
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_InvalidProfile_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := validBody()
	lead := body["lead"].(map[string]any)
	lead["profile"] = "veterinary"
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_InvalidWhatsappNonDigits_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := validBody()
	lead := body["lead"].(map[string]any)
	lead["whatsapp"] = "+55 11 9999-0000"
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_UnknownField_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := validBody()
	body["unexpectedField"] = "value"
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d", rec.Code)
	}
}

func TestHandler_TrailingJSON_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	req := httptest.NewRequest(http.MethodPost, "/api/leads",
		bytes.NewBufferString(`{"source":"lumina-ia-site","submittedAt":"2026-04-16T12:00:00Z","lead":{"name":"João Silva","businessName":"Clínica Saúde+","whatsapp":"11999990000","profile":"medical","consent":true}}{"extra":true}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_Success_Returns200(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{err: nil}, "Bearer secret")
	rec := postLeads(t, h, validBody(), "Bearer secret")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandler_DuplicateLead_Returns409(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{err: leads.ErrDuplicateLead}, "Bearer secret")
	rec := postLeads(t, h, validBody(), "Bearer secret")

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "duplicate_lead" {
		t.Errorf("expected error=duplicate_lead, got %q", resp["error"])
	}
}

func TestHandler_PersistenceFailure_Returns500(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{err: errors.New("db unavailable")}, "Bearer secret")
	rec := postLeads(t, h, validBody(), "Bearer secret")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "internal_error" {
		t.Errorf("expected error=internal_error, got %q", resp["error"])
	}
}
