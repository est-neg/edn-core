package webhookrelay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// relayPathPrefix is the fixed prefix for the InfinitePay webhook path on the public relay.
const relayPathPrefix = "/v1/webhooks/infinitepay/"

// Forwarder sends the raw webhook body to the core internal reconcile route.
type Forwarder interface {
	Forward(ctx context.Context, requestID, receivedAt string, rawBody []byte) (int, error)
}

// Handler is the public relay HTTP handler.
// It validates edge concerns, enforces throttle limits, and forwards raw body to core.
// It has no knowledge of payment semantics, no persistent state, and no provider credentials.
type Handler struct {
	secretPath     string
	nextSecretPath string // optional rotation overlap; empty = disabled
	bodyMaxBytes   int64
	throttle       *Throttle
	forward        Forwarder
	log            *zap.Logger
}

// NewHandler constructs a relay Handler.
func NewHandler(cfg *Config, throttle *Throttle, forward *ForwardClient, log *zap.Logger) *Handler {
	return &Handler{
		secretPath:     cfg.SecretPath,
		nextSecretPath: cfg.NextSecretPath,
		bodyMaxBytes:   cfg.BodyMaxBytes,
		throttle:       throttle,
		forward:        forward,
		log:            log,
	}
}

// ServeHTTP implements http.Handler. It is the single entry point for all requests
// to the relay. Any routing (chi, mux) should pass all POST /v1/webhooks/infinitepay/*
// requests here.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := extractOrGenRequestID(r)

	// 1. Method gate.
	if r.Method != http.MethodPost {
		writeRelayJSON(w, http.StatusMethodNotAllowed, `{"error":"method_not_allowed"}`)
		return
	}

	// 2. Secret path gate (constant-time; NEVER log the path or the expected value).
	if !h.pathAllowed(r.URL.Path) {
		writeRelayJSON(w, http.StatusNotFound, `{"error":"not_found"}`)
		return
	}

	// 3. Content-Type gate.
	// Strict media-type parsing: accept only application/json with no parameters
	// or a single case-insensitive charset=utf-8 parameter (per spec §13.1).
	ct := r.Header.Get("Content-Type")
	if !isAllowedContentType(ct) {
		writeRelayJSON(w, http.StatusUnsupportedMediaType, `{"error":"unsupported_media_type"}`)
		return
	}

	// 4. Body size gate and single-read.
	r.Body = http.MaxBytesReader(w, r.Body, h.bodyMaxBytes)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeRelayJSON(w, http.StatusRequestEntityTooLarge, `{"error":"payload_too_large"}`)
			return
		}
		writeRelayJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}
	if len(rawBody) == 0 {
		writeRelayJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}

	// 5. Normalize source for throttle (uses XFF first, falls back to RemoteAddr).
	source := normalizeSource(r)

	// 6. Throttle gate (per-source in-flight, per-source rate, global in-flight).
	if !h.throttle.Acquire(r.Context(), source) {
		writeRelayJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}
	defer h.throttle.Release(source)

	// 7. Forward raw body to core. Context carries the per-hop timeout set on the HTTP client.
	receivedAt := start.UTC().Format(time.RFC3339Nano)
	coreStatus, fwdErr := h.forward.Forward(r.Context(), requestID, receivedAt, rawBody)
	if fwdErr != nil {
		h.log.Warn("relay forward error",
			zap.String("request_id", requestID),
			zap.Duration("elapsed", time.Since(start)),
			zap.Error(fwdErr),
		)
		writeRelayJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}

	// 8. Synthesize provider-safe response from core status code only.
	// The core response body is never passed through.
	status, body := mapCoreStatus(coreStatus)

	h.log.Info("relay request completed",
		zap.String("request_id", requestID),
		zap.Int("core_status", coreStatus),
		zap.Int("relay_status", status),
		zap.Duration("elapsed", time.Since(start)),
	)
	writeRelayJSON(w, status, body)
}

// pathAllowed returns true when urlPath contains a recognized secret segment.
// Comparison is constant-time to prevent timing-based enumeration.
// The URL path and expected secret values are never logged.
func (h *Handler) pathAllowed(urlPath string) bool {
	if !strings.HasPrefix(urlPath, relayPathPrefix) {
		return false
	}
	candidate := urlPath[len(relayPathPrefix):]
	// Reject paths with additional segments beyond the secret (defense-in-depth).
	if strings.Contains(candidate, "/") {
		return false
	}
	primary := subtle.ConstantTimeCompare([]byte(candidate), []byte(h.secretPath)) == 1
	if h.nextSecretPath != "" {
		next := subtle.ConstantTimeCompare([]byte(candidate), []byte(h.nextSecretPath)) == 1
		return primary || next
	}
	return primary
}

// mapCoreStatus translates the core HTTP status code to the fixed provider-facing response.
//
//	core 200/202 → relay 200 {"status":"ok"}
//		core 422     → relay 422 {"error":"unprocessable_entity"}
//		anything else (5xx, unexpected) → relay 400 {"error":"bad_request"} (transition policy)
func mapCoreStatus(coreStatus int) (int, string) {
	switch {
	case coreStatus == http.StatusOK || coreStatus == http.StatusAccepted:
		return http.StatusOK, `{"status":"ok"}`
	case coreStatus == http.StatusUnprocessableEntity:
		return http.StatusUnprocessableEntity, `{"error":"unprocessable_entity"}`
	default:
		return http.StatusBadRequest, `{"error":"bad_request"}`
	}
}

// isAllowedContentType returns true when ct parses as exactly application/json
// with no parameters or a single case-insensitive charset=utf-8 parameter.
// Any other media type, any other parameter, or a malformed value returns false.
func isAllowedContentType(ct string) bool {
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

// normalizeSource returns the trusted-peer IP from the TCP connection (RemoteAddr) for
// throttle bucketing. X-Forwarded-For is intentionally NOT used — its first hop can be
// set by an attacker before reaching the load balancer, making it an unreliable source
// key for security controls. On Cloud Run behind Google Cloud Load Balancer, fine-grained
// per-originator throttling belongs in a Cloud Armor policy at the network layer.
func normalizeSource(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// extractOrGenRequestID returns the X-Request-ID header value or generates a random ID.
func extractOrGenRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// writeRelayJSON writes the given JSON body with the given status code.
// The body must be a pre-serialized fixed allow-list string; no dynamic content.
func writeRelayJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}
