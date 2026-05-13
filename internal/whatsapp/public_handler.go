package whatsapp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	hubSignatureHeader  = "X-Hub-Signature-256"
	hubSignaturePrefix  = "sha256="
	defaultBodyMaxBytes = int64(3145728) // 3 MiB
)

// sigCheckResult classifies the outcome of signature validation.
type sigCheckResult int

const (
	sigOK        sigCheckResult = iota
	sigAbsent                   // header is missing → 400
	sigMalformed                // header present but wrong format → 400
	sigInvalid                  // header well-formed but HMAC does not match → 403
)

// PublicHandler handles GET (challenge) and POST (notification) on the Meta webhook edge.
// It is stateless: no MongoDB, no Redis, no business logic — only edge validation and relay.
type PublicHandler struct {
	verifyToken  string
	appSecret    []byte
	bodyMaxBytes int64
	forward      Forwarder
	log          *zap.Logger
}

// NewPublicHandler constructs a PublicHandler.
func NewPublicHandler(verifyToken, appSecret string, bodyMaxBytes int64, forward Forwarder, log *zap.Logger) *PublicHandler {
	if bodyMaxBytes <= 0 {
		bodyMaxBytes = defaultBodyMaxBytes
	}
	return &PublicHandler{
		verifyToken:  verifyToken,
		appSecret:    []byte(appSecret),
		bodyMaxBytes: bodyMaxBytes,
		forward:      forward,
		log:          log,
	}
}

// ServeVerify handles GET /v1/webhooks/meta/whatsapp (Meta webhook verification challenge).
//
// Validates hub.mode=subscribe, compares hub.verify_token constant-time against the
// configured secret, and returns 200 with the literal hub.challenge body on success.
// Missing or invalid parameters return 400; wrong token returns 403.
// The verify token is never logged.
func (h *PublicHandler) ServeVerify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "" || token == "" || challenge == "" {
		writePublicJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}
	if mode != "subscribe" {
		writePublicJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.verifyToken)) != 1 {
		writePublicJSON(w, http.StatusForbidden, `{"error":"forbidden"}`)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, challenge) //nolint:errcheck
}

// ServeNotification handles POST /v1/webhooks/meta/whatsapp (incoming notifications).
//
// Guards: Content-Type (415), body size (413), X-Hub-Signature-256 HMAC (400/403).
// On success, forwards the raw body byte-for-byte to the internal intake service.
// Returns 503 when intake is temporarily unavailable; all other internal errors
// are mapped to 200 to avoid leaking state to Meta.
// The app secret and signature header are never logged.
func (h *PublicHandler) ServeNotification(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := genRequestID()

	// 1. Content-Type gate.
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writePublicJSON(w, http.StatusUnsupportedMediaType, `{"error":"unsupported_media_type"}`)
		return
	}

	// 2. Body size gate + single read.
	r.Body = http.MaxBytesReader(w, r.Body, h.bodyMaxBytes)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writePublicJSON(w, http.StatusRequestEntityTooLarge, `{"error":"payload_too_large"}`)
			return
		}
		writePublicJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}

	// 3. HMAC SHA-256 validation — absent/malformed → 400; mismatch → 403.
	switch h.checkSignature(rawBody, r.Header.Get(hubSignatureHeader)) {
	case sigAbsent, sigMalformed:
		writePublicJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	case sigInvalid:
		writePublicJSON(w, http.StatusForbidden, `{"error":"forbidden"}`)
		return
	}

	// 4. Forward raw body byte-for-byte to the internal intake.
	receivedAt := start.UTC().Format(time.RFC3339Nano)
	coreStatus, fwdErr := h.forward.Forward(r.Context(), requestID, receivedAt, rawBody)
	if fwdErr != nil {
		h.log.Warn("whatsapp webhook forward error",
			zap.String("request_id", requestID),
			zap.Duration("elapsed", time.Since(start)),
			zap.Error(fwdErr),
		)
		writePublicJSON(w, http.StatusServiceUnavailable, `{"error":"service_unavailable"}`)
		return
	}

	status, body := mapCoreToPublicStatus(coreStatus)
	h.log.Info("whatsapp webhook handled",
		zap.String("request_id", requestID),
		zap.Int("intake_status", coreStatus),
		zap.Int("relay_status", status),
		zap.Duration("elapsed", time.Since(start)),
	)
	writePublicJSON(w, status, body)
}

// checkSignature validates X-Hub-Signature-256 against HMAC-SHA256 of body.
func (h *PublicHandler) checkSignature(body []byte, sigHeader string) sigCheckResult {
	if sigHeader == "" {
		return sigAbsent
	}
	if !strings.HasPrefix(sigHeader, hubSignaturePrefix) {
		return sigMalformed
	}
	hexSig := sigHeader[len(hubSignaturePrefix):]
	expected, err := hex.DecodeString(hexSig)
	if err != nil || len(expected) == 0 {
		return sigMalformed
	}
	mac := hmac.New(sha256.New, h.appSecret)
	mac.Write(body) //nolint:errcheck
	if !hmac.Equal(mac.Sum(nil), expected) {
		return sigInvalid
	}
	return sigOK
}

// mapCoreToPublicStatus maps the internal intake HTTP status to a Meta-safe relay response.
//
// Only a durable 200 or 202 from the internal service counts as an accepted delivery.
// 400 from internal is surfaced as 400 to signal a bad request (e.g. invalid payload).
// Any other status (401, 403, 404, 409, 413, 415, 5xx) maps to 503 to avoid leaking
// internal state to Meta and to trigger a Meta retry where appropriate.
func mapCoreToPublicStatus(coreStatus int) (int, string) {
	switch coreStatus {
	case http.StatusOK, http.StatusAccepted:
		return http.StatusOK, `{"status":"ok"}`
	case http.StatusBadRequest:
		return http.StatusBadRequest, `{"error":"bad_request"}`
	default:
		return http.StatusServiceUnavailable, `{"error":"service_unavailable"}`
	}
}

// isJSONContentType accepts application/json with optional charset=utf-8.
func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	mediatype, params, err := mime.ParseMediaType(ct)
	if err != nil || mediatype != "application/json" {
		return false
	}
	for k, v := range params {
		if strings.ToLower(k) != "charset" || strings.ToLower(v) != "utf-8" {
			return false
		}
	}
	return true
}

func writePublicJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body) //nolint:errcheck
}

func genRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
