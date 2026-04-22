package leads

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// Handler handles POST /api/leads requests.
type Handler struct {
	service Submitter
	cfg     config.LeadsConfig
	log     *zap.Logger
}

// NewHandler constructs a Handler with its required dependencies.
func NewHandler(service Submitter, cfg config.LeadsConfig, log *zap.Logger) *Handler {
	return &Handler{service: service, cfg: cfg, log: log}
}

type errorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// ServeHTTP satisfies http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Authentication
	if !h.authenticate(r) {
		h.log.Warn("unauthorized lead submission attempt",
			zap.String("request_id", middleware.GetReqID(r.Context())),
		)
		h.writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	// Content-Type guard
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		h.writeJSON(w, http.StatusBadRequest, errorResponse{
			Error:   "invalid_payload",
			Details: "Content-Type must be application/json",
		})
		return
	}

	// Body size limit
	maxBytes := h.cfg.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 16384
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	// Strict JSON decoding — unknown fields are rejected.
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req SubmitRequest
	if err := dec.Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, errorResponse{
			Error:   "invalid_payload",
			Details: err.Error(),
		})
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeJSON(w, http.StatusBadRequest, errorResponse{
			Error:   "invalid_payload",
			Details: "request body must contain exactly one JSON object",
		})
		return
	}

	// Defensive validation
	if err := req.Validate(); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			h.writeJSON(w, http.StatusBadRequest, errorResponse{
				Error:   "invalid_payload",
				Details: ve.Details,
			})
			return
		}
		h.writeJSON(w, http.StatusBadRequest, errorResponse{
			Error:   "invalid_payload",
			Details: err.Error(),
		})
		return
	}

	// Submit to service
	if err := h.service.Submit(r.Context(), req); err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, struct{}{})
}

func (h *Handler) authenticate(r *http.Request) bool {
	header := h.cfg.AuthHeader
	if header == "" {
		header = "Authorization"
	}
	got := r.Header.Get(header)
	expected := h.cfg.AuthToken
	if expected == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func (h *Handler) handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrDuplicateLead):
		h.writeJSON(w, http.StatusConflict, errorResponse{Error: "duplicate_lead"})
	case errors.Is(err, ErrInvalidPayload):
		var ve *ValidationError
		if errors.As(err, &ve) {
			h.writeJSON(w, http.StatusBadRequest, errorResponse{
				Error:   "invalid_payload",
				Details: ve.Details,
			})
			return
		}
		h.writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid_payload"})
	default:
		h.log.Error("lead submission failed",
			zap.String("request_id", middleware.GetReqID(r.Context())),
			zap.Error(err),
		)
		h.writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal_error"})
	}
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
