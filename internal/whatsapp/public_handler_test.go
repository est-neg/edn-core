package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

const (
	testVerifyToken = "test-verify-token-abc123"
	testAppSecret   = "test-app-secret-xyz789"
)

// mockForwarder captures the forwarded request for assertion.
type mockForwarder struct {
	gotBody      []byte
	gotRequestID string
	returnStatus int
	returnErr    error
	calls        int
}

func (m *mockForwarder) Forward(_ context.Context, requestID, _ string, body []byte) (int, error) {
	m.calls++
	m.gotBody = body
	m.gotRequestID = requestID
	return m.returnStatus, m.returnErr
}

func buildPublicHandler(fwd Forwarder) *PublicHandler {
	return NewPublicHandler(testVerifyToken, testAppSecret, 1024, fwd, zap.NewNop())
}

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body) //nolint:errcheck
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func doVerify(h *PublicHandler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/webhooks/meta/whatsapp?"+query, nil)
	rr := httptest.NewRecorder()
	h.ServeVerify(rr, req)
	return rr
}

func doNotify(h *PublicHandler, body []byte, ct, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/meta/whatsapp", bytes.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	rr := httptest.NewRecorder()
	h.ServeNotification(rr, req)
	return rr
}

// ─── GET verification tests ───────────────────────────────────────────────────

func TestPublicHandler_GET_MissingMode_Returns400(t *testing.T) {
	rr := doVerify(buildPublicHandler(nil), "hub.verify_token="+testVerifyToken+"&hub.challenge=abc")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestPublicHandler_GET_MissingToken_Returns400(t *testing.T) {
	rr := doVerify(buildPublicHandler(nil), "hub.mode=subscribe&hub.challenge=abc")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestPublicHandler_GET_MissingChallenge_Returns400(t *testing.T) {
	rr := doVerify(buildPublicHandler(nil), "hub.mode=subscribe&hub.verify_token="+testVerifyToken)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestPublicHandler_GET_InvalidMode_Returns400(t *testing.T) {
	rr := doVerify(buildPublicHandler(nil), "hub.mode=unsubscribe&hub.verify_token="+testVerifyToken+"&hub.challenge=abc")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestPublicHandler_GET_WrongToken_Returns403(t *testing.T) {
	rr := doVerify(buildPublicHandler(nil), "hub.mode=subscribe&hub.verify_token=wrong-token&hub.challenge=abc")
	if rr.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rr.Code)
	}
}

func TestPublicHandler_GET_ValidToken_Returns200WithChallenge(t *testing.T) {
	rr := doVerify(buildPublicHandler(nil), "hub.mode=subscribe&hub.verify_token="+testVerifyToken+"&hub.challenge=challenge-literal-xyz")
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	got := rr.Body.String()
	if got != "challenge-literal-xyz" {
		t.Errorf("body = %q, want %q", got, "challenge-literal-xyz")
	}
}

// ─── POST notification tests ──────────────────────────────────────────────────

func TestPublicHandler_POST_MissingContentType_Returns415(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	sig := signBody(testAppSecret, body)
	rr := doNotify(buildPublicHandler(nil), body, "", sig)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("got %d, want 415", rr.Code)
	}
}

func TestPublicHandler_POST_WrongContentType_Returns415(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	sig := signBody(testAppSecret, body)
	rr := doNotify(buildPublicHandler(nil), body, "text/plain", sig)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("got %d, want 415", rr.Code)
	}
}

func TestPublicHandler_POST_ContentTypeWithUnknownParam_Returns415(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	sig := signBody(testAppSecret, body)
	rr := doNotify(buildPublicHandler(nil), body, "application/json; foo=bar", sig)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("got %d, want 415", rr.Code)
	}
}

func TestPublicHandler_POST_BodyTooLarge_Returns413(t *testing.T) {
	// Handler is configured with bodyMaxBytes=1024; send 1025 bytes.
	body := bytes.Repeat([]byte("x"), 1025)
	sig := signBody(testAppSecret, body)
	rr := doNotify(buildPublicHandler(nil), body, "application/json", sig)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got %d, want 413", rr.Code)
	}
}

func TestPublicHandler_POST_MissingSignature_Returns400(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	rr := doNotify(buildPublicHandler(nil), body, "application/json", "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestPublicHandler_POST_MalformedSignature_NoPrefix_Returns400(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	rr := doNotify(buildPublicHandler(nil), body, "application/json", "not-a-valid-sig")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestPublicHandler_POST_MalformedSignature_BadHex_Returns400(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	rr := doNotify(buildPublicHandler(nil), body, "application/json", "sha256=ZZZnotvalidhex")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestPublicHandler_POST_WrongHMAC_Returns403(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	// Sign with a different secret.
	sig := signBody("wrong-secret", body)
	rr := doNotify(buildPublicHandler(nil), body, "application/json", sig)
	if rr.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rr.Code)
	}
}

func TestPublicHandler_POST_ValidRequest_ForwardsRawBodyByteForByte(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	sig := signBody(testAppSecret, body)

	fwd := &mockForwarder{returnStatus: http.StatusOK}
	rr := doNotify(buildPublicHandler(fwd), body, "application/json", sig)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
	if fwd.calls != 1 {
		t.Errorf("forward calls = %d, want 1", fwd.calls)
	}
	if !bytes.Equal(fwd.gotBody, body) {
		t.Errorf("forwarded body = %q, want %q", fwd.gotBody, body)
	}
}

func TestPublicHandler_POST_ValidRequest_CharsetUTF8Accepted(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	sig := signBody(testAppSecret, body)

	fwd := &mockForwarder{returnStatus: http.StatusOK}
	rr := doNotify(buildPublicHandler(fwd), body, "application/json; charset=utf-8", sig)

	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
}

func TestPublicHandler_POST_IntakeServiceUnavailable_Returns503(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	sig := signBody(testAppSecret, body)

	fwd := &mockForwarder{returnStatus: http.StatusServiceUnavailable}
	rr := doNotify(buildPublicHandler(fwd), body, "application/json", sig)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
}

// ─── isJSONContentType unit tests ────────────────────────────────────────────

func TestIsJSONContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"application/json; charset=UTF-8", true},
		{"", false},
		{"text/plain", false},
		{"application/json; foo=bar", false},
		{"application/json; charset=latin-1", false},
	}
	for _, tc := range tests {
		got := isJSONContentType(tc.ct)
		if got != tc.want {
			t.Errorf("isJSONContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

// ─── checkSignature unit tests ────────────────────────────────────────────────

func TestCheckSignature_Absent(t *testing.T) {
	h := buildPublicHandler(nil)
	if got := h.checkSignature([]byte("body"), ""); got != sigAbsent {
		t.Errorf("got %v, want sigAbsent", got)
	}
}

func TestCheckSignature_Malformed_NoPrefix(t *testing.T) {
	h := buildPublicHandler(nil)
	if got := h.checkSignature([]byte("body"), "notaprefix=abc"); got != sigMalformed {
		t.Errorf("got %v, want sigMalformed", got)
	}
}

func TestCheckSignature_Malformed_BadHex(t *testing.T) {
	h := buildPublicHandler(nil)
	if got := h.checkSignature([]byte("body"), "sha256=zzznotvalidhex"); got != sigMalformed {
		t.Errorf("got %v, want sigMalformed", got)
	}
}

func TestCheckSignature_Invalid(t *testing.T) {
	h := buildPublicHandler(nil)
	sig := signBody("wrong-secret", []byte("body"))
	if got := h.checkSignature([]byte("body"), sig); got != sigInvalid {
		t.Errorf("got %v, want sigInvalid", got)
	}
}

func TestCheckSignature_OK(t *testing.T) {
	h := buildPublicHandler(nil)
	body := []byte("some body content")
	sig := signBody(testAppSecret, body)
	if got := h.checkSignature(body, sig); got != sigOK {
		t.Errorf("got %v, want sigOK", got)
	}
}

// ─── mapCoreToPublicStatus tests ──────────────────────────────────────────────

func TestMapCoreToPublicStatus(t *testing.T) {
	tests := []struct {
		core       int
		wantStatus int
	}{
		{200, 200},
		{202, 200},
		{400, 400},
		{401, 503},
		{403, 503},
		{404, 503},
		{409, 503},
		{500, 503},
		{503, 503},
	}
	for _, tc := range tests {
		got, _ := mapCoreToPublicStatus(tc.core)
		if got != tc.wantStatus {
			t.Errorf("mapCoreToPublicStatus(%d) = %d, want %d", tc.core, got, tc.wantStatus)
		}
	}
}

func TestPublicHandler_POST_IntakeBadRequest_Returns400(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	sig := signBody(testAppSecret, body)

	fwd := &mockForwarder{returnStatus: http.StatusBadRequest}
	rr := doNotify(buildPublicHandler(fwd), body, "application/json", sig)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 when internal returns 400", rr.Code)
	}
}

func TestPublicHandler_POST_IntakeAuthError_Returns503(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	sig := signBody(testAppSecret, body)

	// 401 from internal must map to 503 (not leak auth state to Meta).
	fwd := &mockForwarder{returnStatus: http.StatusUnauthorized}
	rr := doNotify(buildPublicHandler(fwd), body, "application/json", sig)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503 when internal returns 401", rr.Code)
	}
}

func TestPublicHandler_POST_Intake403_Returns503(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	sig := signBody(testAppSecret, body)

	fwd := &mockForwarder{returnStatus: http.StatusForbidden}
	rr := doNotify(buildPublicHandler(fwd), body, "application/json", sig)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503 when internal returns 403", rr.Code)
	}
}

// ─── Response body tests ──────────────────────────────────────────────────────

func TestPublicHandler_GET_ResponseBodyIsPlainText(t *testing.T) {
	rr := doVerify(buildPublicHandler(nil), "hub.mode=subscribe&hub.verify_token="+testVerifyToken+"&hub.challenge=my-challenge")
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if body := rr.Body.String(); body != "my-challenge" {
		t.Errorf("body = %q, want %q", body, "my-challenge")
	}
}

func TestPublicHandler_POST_Response_ContainsJSON(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	sig := signBody(testAppSecret, body)
	fwd := &mockForwarder{returnStatus: http.StatusOK}
	rr := doNotify(buildPublicHandler(fwd), body, "application/json", sig)

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	respBody := rr.Body.String()
	if !strings.Contains(respBody, `"status"`) {
		t.Errorf("body = %q, expected JSON with status field", respBody)
	}
}

// ensure io.Writer usage via io.WriteString doesn't break anything
var _ io.Writer = (*httptest.ResponseRecorder)(nil)
