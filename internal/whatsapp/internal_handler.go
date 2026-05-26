package whatsapp

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const internalTokenHeader = "X-EDN-Internal-Token"

// InternalHandler handles POST /internal/whatsapp/meta/messages/intake.
//
// Cloud Run service-to-service IAM is the primary authentication control.
// X-EDN-Internal-Token is an optional defense-in-depth secondary check.
// It has MongoDB access, performs message parsing, dedupe, and durable persistence.
type InternalHandler struct {
	service       IntakeProcessor
	internalToken string // empty = secondary token check disabled
	bodyMaxBytes  int64
	log           *zap.Logger
}

// NewInternalHandler constructs an InternalHandler.
func NewInternalHandler(service IntakeProcessor, internalToken string, bodyMaxBytes int64, log *zap.Logger) *InternalHandler {
	if bodyMaxBytes <= 0 {
		bodyMaxBytes = defaultBodyMaxBytes
	}
	return &InternalHandler{
		service:       service,
		internalToken: internalToken,
		bodyMaxBytes:  bodyMaxBytes,
		log:           log,
	}
}

// ServeHTTP implements http.Handler.
func (h *InternalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-EDN-Relay-Request-ID")
	if requestID == "" {
		requestID = genRequestID()
	}

	// Optional defense-in-depth secondary token check (IAM is the primary control).
	// The token value is never logged.
	if h.internalToken != "" {
		tok := r.Header.Get(internalTokenHeader)
		if subtle.ConstantTimeCompare([]byte(tok), []byte(h.internalToken)) != 1 {
			writePublicJSON(w, http.StatusUnauthorized, `{"error":"unauthorized"}`)
			return
		}
	}

	// Body size gate + single read.
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

	var payload MetaWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		writePublicJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
		return
	}

	// Derive received_at from the relay correlation header; fall back to now.
	receivedAt := time.Now().UTC()
	if ts := r.Header.Get("X-EDN-Relay-Received-At"); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			receivedAt = parsed.UTC()
		}
	}

	// Count messages to decide whether to write anything.
	msgCount := 0
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			msgCount += len(change.Value.Messages)
		}
	}

	if msgCount == 0 {
		h.log.Info("whatsapp intake: no messages in payload, ignored",
			zap.String("request_id", requestID),
		)
		writePublicJSON(w, http.StatusOK, `{"status":"ignored"}`)
		return
	}

	if err := h.service.ProcessMessages(r.Context(), &IntakeInput{
		IngressRequestID: requestID,
		ReceivedAt:       receivedAt,
		RawPayload:       rawBody,
		Payload:          &payload,
	}); err != nil {
		if errors.Is(err, ErrInvalidPayload) {
			h.log.Warn("whatsapp intake: invalid payload",
				zap.String("request_id", requestID),
			)
			writePublicJSON(w, http.StatusBadRequest, `{"error":"bad_request"}`)
			return
		}
		h.log.Error("whatsapp intake: processing failed",
			zap.String("request_id", requestID),
		)
		writePublicJSON(w, http.StatusServiceUnavailable, `{"error":"service_unavailable"}`)
		return
	}

	writePublicJSON(w, http.StatusOK, `{"status":"ok"}`)
}
