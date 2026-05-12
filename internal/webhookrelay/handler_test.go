package webhookrelay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// fwdFunc implements Forwarder via a plain function for test convenience.
type fwdFunc func(ctx context.Context, requestID, receivedAt string, rawBody []byte) (int, error)

func (f fwdFunc) Forward(ctx context.Context, requestID, receivedAt string, rawBody []byte) (int, error) {
	return f(ctx, requestID, receivedAt, rawBody)
}

// buildHandler creates a relay Handler backed by a real Throttle and the given Forwarder.
func buildHandler(secretPath string, fwd Forwarder) *Handler {
	throttle := NewThrottle(8, 120, 32)
	return &Handler{
		secretPath:   secretPath,
		bodyMaxBytes: 65536,
		throttle:     throttle,
		forward:      fwd,
		log:          zap.NewNop(),
	}
}

func validWebhookBody() []byte {
	return []byte(`{"transaction_id":"tx-123","order_id":"order-456","invoice_id":"inv-789"}`)
}

func doRequest(t *testing.T, h *Handler, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.RemoteAddr = "10.0.0.1:12345"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// ─── Method gate ─────────────────────────────────────────────────────────────

func TestRelayHandler_MethodNotAllowed(t *testing.T) {
	var calls int
	h := buildHandler("test-secret-abc123", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		calls++
		return http.StatusOK, nil
	}))
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rr := doRequest(t, h, method, "/v1/webhooks/infinitepay/test-secret-abc123", "application/json", validWebhookBody())
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", method, rr.Code)
		}
	}
	if calls != 0 {
		t.Errorf("expected no forward calls on method not allowed, got %d", calls)
	}
}

// ─── Secret path gate ─────────────────────────────────────────────────────────

func TestRelayHandler_WrongSecretPath_Returns404(t *testing.T) {
	var calls int
	h := buildHandler("correct-secret-xyz", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		calls++
		return http.StatusOK, nil
	}))
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/wrong-secret", "application/json", validWebhookBody())
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for wrong secret, got %d", rr.Code)
	}
	if calls != 0 {
		t.Errorf("expected no forward calls on wrong path, got %d", calls)
	}
}

func TestRelayHandler_CorrectSecretPath_Forwards(t *testing.T) {
	h := buildHandler("correct-secret-xyz", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		return http.StatusOK, nil
	}))
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/correct-secret-xyz", "application/json", validWebhookBody())
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRelayHandler_NextSecretPath_AllowsRotationOverlap(t *testing.T) {
	throttle := NewThrottle(8, 120, 32)
	var calls int
	h := &Handler{
		secretPath:     "primary-secret",
		nextSecretPath: "rotation-secret",
		bodyMaxBytes:   65536,
		throttle:       throttle,
		log:            zap.NewNop(),
		forward: fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
			calls++
			return http.StatusOK, nil
		}),
	}

	// Both primary and rotation paths should be accepted.
	for _, path := range []string{
		"/v1/webhooks/infinitepay/primary-secret",
		"/v1/webhooks/infinitepay/rotation-secret",
	} {
		rr := doRequest(t, h, http.MethodPost, path, "application/json", validWebhookBody())
		if rr.Code != http.StatusOK {
			t.Errorf("path %s: expected 200, got %d", path, rr.Code)
		}
	}
	if calls != 2 {
		t.Errorf("expected 2 forward calls, got %d", calls)
	}
}

// ─── Content-Type gate ────────────────────────────────────────────────────────

func TestRelayHandler_WrongContentType_Returns415(t *testing.T) {
	var calls int
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		calls++
		return http.StatusOK, nil
	}))
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", ct, validWebhookBody())
		if rr.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: expected 415, got %d", ct, rr.Code)
		}
	}
	if calls != 0 {
		t.Errorf("expected no forward calls on wrong content-type, got %d", calls)
	}
}

func TestRelayHandler_ContentTypeWithCharset_Accepted(t *testing.T) {
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		return http.StatusOK, nil
	}))
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json; charset=utf-8", validWebhookBody())
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for application/json; charset=utf-8, got %d", rr.Code)
	}
}

// ─── Body size gate ───────────────────────────────────────────────────────────

func TestRelayHandler_BodyTooLarge_Returns413(t *testing.T) {
	var calls int
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		calls++
		return http.StatusOK, nil
	}))
	h.bodyMaxBytes = 10 // very small limit for the test
	large := bytes.Repeat([]byte("x"), 100)
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", large)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rr.Code)
	}
	if calls != 0 {
		t.Errorf("expected no forward calls on oversized body, got %d", calls)
	}
}

// ─── Core response mapping ────────────────────────────────────────────────────

func TestRelayHandler_Core200_Returns200(t *testing.T) {
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		return http.StatusOK, nil
	}))
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", validWebhookBody())
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Errorf("expected ok in body, got %s", rr.Body.String())
	}
}

func TestRelayHandler_Core422_Returns422(t *testing.T) {
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		return http.StatusUnprocessableEntity, nil
	}))
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", validWebhookBody())
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", rr.Code)
	}
	// Must not expose core error detail in relay response.
	if strings.Contains(rr.Body.String(), "transaction") || strings.Contains(rr.Body.String(), "amount") {
		t.Errorf("relay must not expose core error details: %s", rr.Body.String())
	}
}

func TestRelayHandler_Core500_Returns400_TransitionPolicy(t *testing.T) {
	for _, coreStatus := range []int{500, 503, 502, 504} {
		cs := coreStatus // capture
		h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
			return cs, nil
		}))
		rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", validWebhookBody())
		if rr.Code != http.StatusBadRequest {
			t.Errorf("core %d: expected provider-facing 400 under transition policy, got %d", cs, rr.Code)
		}
	}
}

func TestRelayHandler_ForwardNetworkError_Returns400(t *testing.T) {
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		return 0, io.ErrUnexpectedEOF
	}))
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", validWebhookBody())
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on network error, got %d", rr.Code)
	}
}

// ─── Raw body pass-through ────────────────────────────────────────────────────

func TestRelayHandler_ForwardsRawBodyByteForByte(t *testing.T) {
	original := []byte(`{"transaction_id":"tx-abc","order_id":"ord-xyz","invoice_id":"inv-001","extra_field":true}`)
	var captured []byte
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, rawBody []byte) (int, error) {
		captured = make([]byte, len(rawBody))
		copy(captured, rawBody)
		return http.StatusOK, nil
	}))
	doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", original)
	if !bytes.Equal(original, captured) {
		t.Errorf("forwarded body mismatch\nwant: %s\ngot:  %s", original, captured)
	}
}

// ─── Response body isolation ──────────────────────────────────────────────────

func TestRelayHandler_ResponseDoesNotLeakCoreBody(t *testing.T) {
	// Simulate a core 422 with an internal error message that must NOT reach the provider.
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":"transaction identity mismatch","order_nsu":"secret-order"}`)
	}))
	defer core.Close()

	cfg := &Config{
		SecretPath:        "my-secret",
		CoreURL:           core.URL + "/internal/payments/providers/infinitepay/webhook-reconcile",
		BodyMaxBytes:      65536,
		ForwardTimeoutSec: 3,
		SourceMaxInFlight: 8,
		SourceRPM:         120,
		GlobalMaxInFlight: 32,
	}
	fwdClient := NewForwardClient(cfg.CoreURL, "", cfg.ForwardTimeoutSec, NewStaticTokenProvider(""))
	throttle := NewThrottle(cfg.SourceMaxInFlight, cfg.SourceRPM, cfg.GlobalMaxInFlight)
	h := NewHandler(cfg, throttle, fwdClient, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/infinitepay/my-secret", bytes.NewReader(validWebhookBody()))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "order_nsu") || strings.Contains(body, "secret-order") || strings.Contains(body, "identity mismatch") {
		t.Errorf("relay must not expose core response body to provider, got: %s", body)
	}
}

// ─── Unit test for mapCoreStatus ─────────────────────────────────────────────

func TestMapCoreStatus(t *testing.T) {
	tests := []struct {
		coreStatus   int
		wantStatus   int
		wantBodyPart string
	}{
		{200, 200, "ok"},
		{202, 200, "ok"},
		{422, 422, "unprocessable_entity"},
		{500, 400, "bad_request"},
		{503, 400, "bad_request"},
		{502, 400, "bad_request"},
		{429, 400, "bad_request"},
		{401, 400, "bad_request"},
	}
	for _, tc := range tests {
		status, body := mapCoreStatus(tc.coreStatus)
		if status != tc.wantStatus {
			t.Errorf("coreStatus %d: want relay status %d, got %d", tc.coreStatus, tc.wantStatus, status)
		}
		if !strings.Contains(body, tc.wantBodyPart) {
			t.Errorf("coreStatus %d: want body containing %q, got %q", tc.coreStatus, tc.wantBodyPart, body)
		}
	}
}

// ─── Unit test for normalizeSource ───────────────────────────────────────────

// TestNormalizeSource verifies that normalizeSource always returns the RemoteAddr
// host, ignoring X-Forwarded-For (which is attacker-controllable).
func TestNormalizeSource(t *testing.T) {
	tests := []struct {
		xff        string
		remoteAddr string
		want       string
	}{
		// XFF present but must be ignored; RemoteAddr is the trusted value.
		{"203.0.113.1", "10.0.0.1:12345", "10.0.0.1"},
		{"203.0.113.1, 10.0.0.2", "10.0.0.3:12345", "10.0.0.3"},
		{"", "10.0.0.2:54321", "10.0.0.2"},
		{"", "[::1]:80", "::1"},
	}
	for _, tc := range tests {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		r.RemoteAddr = tc.remoteAddr
		got := normalizeSource(r)
		if got != tc.want {
			t.Errorf("xff=%q remoteAddr=%q: want %q, got %q", tc.xff, tc.remoteAddr, tc.want, got)
		}
	}
}

// ─── Strict Content-Type validation ──────────────────────────────────────────

// TestRelayHandler_StrictContentType_RejectsExtraParams verifies that content types
// with parameters other than charset=utf-8 are rejected with 415.
func TestRelayHandler_StrictContentType_RejectsExtraParams(t *testing.T) {
	var calls int
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		calls++
		return http.StatusOK, nil
	}))
	rejected := []string{
		"application/json; charset=iso-8859-1",
		"application/json; x-custom=foo",
		"application/json; charset=utf-8; extra=bar",
		"application/json-patch+json",
		"application/json+xml",
	}
	for _, ct := range rejected {
		rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", ct, validWebhookBody())
		if rr.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: expected 415, got %d", ct, rr.Code)
		}
	}
	if calls != 0 {
		t.Errorf("expected no forward calls for rejected content types, got %d", calls)
	}
}

// TestRelayHandler_StrictContentType_AcceptsExactApplicationJSON verifies that
// application/json without parameters is accepted.
func TestRelayHandler_StrictContentType_AcceptsExactApplicationJSON(t *testing.T) {
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		return http.StatusOK, nil
	}))
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", validWebhookBody())
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for application/json, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ─── Header allow-list / no pass-through ─────────────────────────────────────

// TestForwardClient_HeaderAllowList_ForbiddenHeadersNotForwarded verifies that
// inbound provider headers (Authorization, X-EDN-*, X-Forwarded-For, Forwarded)
// are never forwarded to the core service.
// The relay handler strips all such headers before the ForwardClient builds its request.
func TestForwardClient_HeaderAllowList_ForbiddenHeadersNotForwarded(t *testing.T) {
	var (
		mu              sync.Mutex
		capturedHeaders http.Header
	)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedHeaders = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer core.Close()

	cfg := &Config{
		SecretPath:        "test-secret",
		CoreURL:           core.URL + "/internal/payments/providers/infinitepay/webhook-reconcile",
		BodyMaxBytes:      65536,
		ForwardTimeoutSec: 3,
		SourceMaxInFlight: 8,
		SourceRPM:         120,
		GlobalMaxInFlight: 32,
	}
	fwdClient := NewForwardClient(cfg.CoreURL, "", cfg.ForwardTimeoutSec, NewStaticTokenProvider(""))
	throttle := NewThrottle(cfg.SourceMaxInFlight, cfg.SourceRPM, cfg.GlobalMaxInFlight)
	h := NewHandler(cfg, throttle, fwdClient, zap.NewNop())

	// Build a request that includes headers the relay must strip.
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/infinitepay/test-secret", bytes.NewReader(validWebhookBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer provider-secret-must-not-reach-core")
	req.Header.Set("X-EDN-Internal-Token", "internal-token-must-not-pass-through")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Forwarded", "for=1.2.3.4")
	req.Header.Set("Connection", "keep-alive")
	req.RemoteAddr = "10.0.0.1:12345"

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()

	forbidden := []string{
		"Authorization",
		"X-Edn-Internal-Token",
		"Forwarded",
	}
	for _, hdr := range forbidden {
		if v := capturedHeaders.Get(hdr); v != "" {
			t.Errorf("forbidden header %q must not be forwarded to core, got %q", hdr, v)
		}
	}
	// X-Forwarded-For from provider must not be forwarded unchanged.
	if v := capturedHeaders.Get("X-Forwarded-For"); v == "1.2.3.4" {
		t.Errorf("inbound X-Forwarded-For must not be passed unchanged to core")
	}

	// Verify the allow-listed correlation header is present.
	if capturedHeaders.Get("X-Edn-Relay-Request-Id") == "" && capturedHeaders.Get("X-Edn-Relay-Request-ID") == "" {
		// Header name is canonicalized — check the raw map.
		found := false
		for k := range capturedHeaders {
			if strings.EqualFold(k, "X-EDN-Relay-Request-ID") {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected X-EDN-Relay-Request-ID correlation header to be forwarded to core")
		}
	}
	if capturedHeaders.Get("Content-Type") == "" {
		t.Error("expected Content-Type to be forwarded to core")
	}
}

// ─── Primary auth: bearer token from TokenProvider ───────────────────────────

// TestForwardClient_BearerToken_AttachedWhenProviderConfigured verifies that when a
// non-empty StaticTokenProvider is configured, the ForwardClient attaches
// "Authorization: Bearer <token>" to the outbound core request.
func TestForwardClient_BearerToken_AttachedWhenProviderConfigured(t *testing.T) {
	var (
		mu           sync.Mutex
		capturedAuth string
	)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer core.Close()

	const testToken = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.test"
	fwdClient := NewForwardClient(
		core.URL+"/internal/payments/providers/infinitepay/webhook-reconcile",
		"",
		3,
		NewStaticTokenProvider(testToken),
	)

	status, err := fwdClient.Forward(context.Background(), "req-1", "2026-01-01T00:00:00Z", validWebhookBody())
	if err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}

	mu.Lock()
	defer mu.Unlock()
	if capturedAuth != "Bearer "+testToken {
		t.Errorf("expected Authorization header %q, got %q", "Bearer "+testToken, capturedAuth)
	}
}

// TestForwardClient_BearerToken_NotAttachedWhenProviderDisabled verifies that when
// the StaticTokenProvider holds an empty token, no Authorization header is sent.
func TestForwardClient_BearerToken_NotAttachedWhenProviderDisabled(t *testing.T) {
	var (
		mu           sync.Mutex
		capturedAuth string
	)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer core.Close()

	fwdClient := NewForwardClient(
		core.URL+"/internal/payments/providers/infinitepay/webhook-reconcile",
		"",
		3,
		NewStaticTokenProvider(""), // disabled — local dev
	)

	status, err := fwdClient.Forward(context.Background(), "req-2", "2026-01-01T00:00:00Z", validWebhookBody())
	if err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}

	mu.Lock()
	defer mu.Unlock()
	if capturedAuth != "" {
		t.Errorf("expected no Authorization header, got %q", capturedAuth)
	}
}

// TestForwardClient_BearerToken_ProviderError_Returns0 verifies that a token
// provider error causes Forward to return a non-nil error (not a spurious 200).
func TestForwardClient_BearerToken_ProviderError_Returns0(t *testing.T) {
	errProvider := &errorTokenProvider{err: errors.New("metadata unavailable")}
	fwdClient := NewForwardClient(
		"http://127.0.0.1:0/unreachable",
		"",
		3,
		errProvider,
	)
	status, err := fwdClient.Forward(context.Background(), "req-3", "2026-01-01T00:00:00Z", validWebhookBody())
	if err == nil {
		t.Fatal("expected error from token provider failure, got nil")
	}
	if status != 0 {
		t.Errorf("expected status 0 on token provider error, got %d", status)
	}
}

// errorTokenProvider is a test-only TokenProvider that always returns an error.
type errorTokenProvider struct{ err error }

func (e *errorTokenProvider) Token(_ context.Context) (string, error) { return "", e.err }

// ─── Relay hardening: throttle saturation ────────────────────────────────────

// TestRelayHandler_ThrottleSaturated_Returns400BadRequest proves that when the global
// in-flight capacity is exhausted, the relay sheds new requests with provider-facing
// 400 / bad_request rather than queuing or returning false success.
func TestRelayHandler_ThrottleSaturated_Returns400BadRequest(t *testing.T) {
	// Create a throttle with a global limit of 1 so a single manual acquire saturates it.
	throttle := NewThrottle(8, 120, 1)
	h := &Handler{
		secretPath:   "my-secret",
		bodyMaxBytes: 65536,
		throttle:     throttle,
		log:          zap.NewNop(),
		forward: fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
			return http.StatusOK, nil
		}),
	}

	// Saturate the single global slot directly — simulates a request already in flight.
	if !throttle.Acquire(context.Background(), "saturator") {
		t.Fatal("pre-condition failed: could not acquire the initial slot to saturate throttle")
	}
	defer throttle.Release("saturator")

	// Next inbound request must be shed with 400 / bad_request (backpressure).
	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", validWebhookBody())
	if rr.Code != http.StatusBadRequest {
		t.Errorf("throttle saturation: expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_request") {
		t.Errorf("throttle saturation: expected bad_request in body, got %s", rr.Body.String())
	}
}

// ─── Relay hardening: slow-core timeout ──────────────────────────────────────

// TestRelayHandler_SlowCore_ContextTimeout_Returns400 proves that when the core is slow
// and the forward times out (context.DeadlineExceeded), the relay returns the
// transition-policy temporary failure response (400 / bad_request) rather than
// false success or a 5xx that would confuse the provider retry logic.
func TestRelayHandler_SlowCore_ContextTimeout_Returns400(t *testing.T) {
	h := buildHandler("my-secret", fwdFunc(func(_ context.Context, _, _ string, _ []byte) (int, error) {
		// Simulate core not responding within the forward timeout.
		return 0, context.DeadlineExceeded
	}))

	rr := doRequest(t, h, http.MethodPost, "/v1/webhooks/infinitepay/my-secret", "application/json", validWebhookBody())
	if rr.Code != http.StatusBadRequest {
		t.Errorf("slow-core timeout: expected transition-policy 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_request") {
		t.Errorf("slow-core timeout: expected bad_request in body, got %s", rr.Body.String())
	}
}
