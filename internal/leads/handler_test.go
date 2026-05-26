package leads_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		"name":   "João Silva",
		"email":  "joao@example.com",
		"phone":  "+5511999990000",
		"source": "test",
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

func TestHandler_MissingEmail_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := validBody()
	delete(body, "email")
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

func TestHandler_InvalidEmail_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := validBody()
	body["email"] = "not-an-email"
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
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
		bytes.NewBufferString(`{"name":"João Silva","email":"joao@example.com"}{"extra":true}`),
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

// --- capturing submitter for normalization tests ---

type capturingSubmitter struct {
	req leads.SubmitRequest
	err error
}

func (c *capturingSubmitter) Submit(_ context.Context, req leads.SubmitRequest) error {
	c.req = req
	return c.err
}

// --- nested payload tests ---

func nestedBody() map[string]any {
	return map[string]any{
		"source":      "estaleiro-site",
		"submittedAt": "2026-05-26T10:11:12Z",
		"lead": map[string]any{
			"name":         "Joao Silva",
			"businessName": "Clinica X",
			"whatsapp":     "11999990000",
			"email":        "joao@example.com",
			"profile":      "medical",
			"message":      "Oi",
			"consent":      true,
		},
	}
}

func TestHandler_FlatPayload_NormalizesAndReturns200(t *testing.T) {
	cs := &capturingSubmitter{}
	h := newHandler(t, cs, "Bearer secret")
	rec := postLeads(t, h, validBody(), "Bearer secret")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if cs.req.Name != "João Silva" {
		t.Errorf("expected Name=João Silva, got %q", cs.req.Name)
	}
	if cs.req.Email != "joao@example.com" {
		t.Errorf("expected Email=joao@example.com, got %q", cs.req.Email)
	}
	// validBody sends "+5511999990000"; backend must normalize to digits only.
	if cs.req.Phone != "5511999990000" {
		t.Errorf("expected Phone=5511999990000 (normalized), got %q", cs.req.Phone)
	}
	if cs.req.ParsedSubmittedAt != nil {
		t.Error("expected ParsedSubmittedAt to be nil for flat payload")
	}
}

func TestHandler_NestedPayload_Returns200AndNormalizes(t *testing.T) {
	cs := &capturingSubmitter{}
	h := newHandler(t, cs, "Bearer secret")
	rec := postLeads(t, h, nestedBody(), "Bearer secret")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if cs.req.Name != "Joao Silva" {
		t.Errorf("expected Name=Joao Silva, got %q", cs.req.Name)
	}
	if cs.req.Email != "joao@example.com" {
		t.Errorf("expected Email=joao@example.com, got %q", cs.req.Email)
	}
	if cs.req.Phone != "11999990000" {
		t.Errorf("expected Phone=11999990000 (normalized), got %q", cs.req.Phone)
	}
	if cs.req.BusinessName != "Clinica X" {
		t.Errorf("expected BusinessName=Clinica X, got %q", cs.req.BusinessName)
	}
	if cs.req.Profile != "medical" {
		t.Errorf("expected Profile=medical, got %q", cs.req.Profile)
	}
	if cs.req.ParsedSubmittedAt == nil {
		t.Fatal("expected ParsedSubmittedAt to be set")
	}
	if cs.req.ParsedSubmittedAt.IsZero() {
		t.Error("expected ParsedSubmittedAt to be non-zero")
	}
	if !cs.req.Consent {
		t.Error("expected Consent=true")
	}
}

func TestHandler_NestedPayload_InvalidSubmittedAt_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := nestedBody()
	body["submittedAt"] = "not-a-date"
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

func TestHandler_MixedPayload_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := map[string]any{
		"name":        "Joao",
		"email":       "joao@example.com",
		"source":      "estaleiro-site",
		"submittedAt": "2026-05-26T10:11:12Z",
		"lead": map[string]any{
			"name":    "Joao",
			"email":   "joao@example.com",
			"consent": true,
		},
	}
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

func TestHandler_NestedConsentFalse_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := nestedBody()
	body["lead"].(map[string]any)["consent"] = false
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

func TestHandler_NestedPayload_MissingLead_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := map[string]any{
		"source":      "estaleiro-site",
		"submittedAt": "2026-05-26T10:11:12Z",
		// lead is absent
	}
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

// TestHandler_FlatPhone_NormalizesToSameValueAsNested shows that "+55 11 99999-0000"
// submitted via the flat path produces the same internal Phone string as "11999990000"
// submitted via the nested whatsapp path.
func TestHandler_FlatAndNested_PhoneNormalizationEquivalent(t *testing.T) {
	flatCS := &capturingSubmitter{}
	fh := newHandler(t, flatCS, "Bearer secret")
	flatBody := map[string]any{
		"name":  "Test User",
		"email": "test@example.com",
		"phone": "+55 11 99999-0000",
	}
	if rec := postLeads(t, fh, flatBody, "Bearer secret"); rec.Code != http.StatusOK {
		t.Fatalf("flat: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	nestedCS := &capturingSubmitter{}
	nh := newHandler(t, nestedCS, "Bearer secret")
	nb := nestedBody()
	nb["lead"].(map[string]any)["whatsapp"] = "+55 11 99999-0000"
	if rec := postLeads(t, nh, nb, "Bearer secret"); rec.Code != http.StatusOK {
		t.Fatalf("nested: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if flatCS.req.Phone != nestedCS.req.Phone {
		t.Errorf("phone mismatch: flat=%q nested=%q", flatCS.req.Phone, nestedCS.req.Phone)
	}
}

func TestHandler_FlatPhone_TooShort_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := map[string]any{
		"name":  "Test User",
		"email": "test@example.com",
		"phone": "1234567", // 7 digits — below minimum
	}
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

func TestHandler_FlatSource_TooLong_Returns400(t *testing.T) {
	h := newHandler(t, &fakeSubmitter{}, "Bearer secret")
	body := map[string]any{
		"name":   "Test User",
		"email":  "test@example.com",
		"source": strings.Repeat("x", 81),
	}
	rec := postLeads(t, h, body, "Bearer secret")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	resp := decodeError(t, rec)
	if resp["error"] != "invalid_payload" {
		t.Errorf("expected error=invalid_payload, got %q", resp["error"])
	}
}

