package organizations

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for the organizations resource.
type Handler struct {
	repo      Repository
	authToken string
	log       *zap.Logger
}

// NewHandler constructs a Handler with its required dependencies.
// authToken is the full expected Authorization header value (e.g. "Bearer <token>").
func NewHandler(repo Repository, authToken string, log *zap.Logger) *Handler {
	return &Handler{repo: repo, authToken: authToken, log: log}
}

type errResponse struct {
	Error string `json:"error"`
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) authenticate(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	if h.authToken == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.authToken)) == 1
}

// List handles GET /api/v1/organizations.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	orgs, err := h.repo.List(r.Context())
	if err != nil {
		h.log.Error("list organizations", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusOK, orgs)
}

// createRequest is the request body for creating an organization.
type createRequest struct {
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	LegalName    string  `json:"legal_name"`
	CNPJ         string  `json:"cnpj,omitempty"`
	BillingEmail string  `json:"billing_email"`
	Phone        string  `json:"phone,omitempty"`
	Address      Address `json:"address"`
}

// Create handles POST /api/v1/organizations.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid_payload"})
		return
	}
	if req.Slug == "" || req.Name == "" || req.BillingEmail == "" {
		h.writeJSON(w, http.StatusBadRequest, errResponse{Error: "slug, name and billing_email are required"})
		return
	}
	now := time.Now().UTC()
	org := Organization{
		OrgUUID:      uuid.New().String(),
		Slug:         req.Slug,
		Name:         req.Name,
		LegalName:    req.LegalName,
		CNPJ:         req.CNPJ,
		BillingEmail: req.BillingEmail,
		Phone:        req.Phone,
		Address:      req.Address,
		Active:       true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.repo.Create(r.Context(), org); err != nil {
		if errors.Is(err, ErrDuplicate) {
			h.writeJSON(w, http.StatusConflict, errResponse{Error: "duplicate"})
			return
		}
		h.log.Error("create organization", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusCreated, org)
}

// GetByUUID handles GET /api/v1/organizations/{orgUUID}.
func (h *Handler) GetByUUID(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	orgUUID := chi.URLParam(r, "orgUUID")
	org, err := h.repo.FindByUUID(r.Context(), orgUUID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, errResponse{Error: "not_found"})
			return
		}
		h.log.Error("find organization", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusOK, org)
}

// updateRequest is the request body for updating an organization.
type updateRequest struct {
	Name         string  `json:"name"`
	LegalName    string  `json:"legal_name"`
	CNPJ         string  `json:"cnpj,omitempty"`
	BillingEmail string  `json:"billing_email"`
	Phone        string  `json:"phone,omitempty"`
	Address      Address `json:"address"`
	Active       bool    `json:"active"`
}

// Update handles PUT /api/v1/organizations/{orgUUID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	orgUUID := chi.URLParam(r, "orgUUID")
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid_payload"})
		return
	}
	patch := Organization{
		Name:         req.Name,
		LegalName:    req.LegalName,
		CNPJ:         req.CNPJ,
		BillingEmail: req.BillingEmail,
		Phone:        req.Phone,
		Address:      req.Address,
		Active:       req.Active,
	}
	if err := h.repo.Update(r.Context(), orgUUID, patch); err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, errResponse{Error: "not_found"})
			return
		}
		h.log.Error("update organization", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// Deactivate handles DELETE /api/v1/organizations/{orgUUID}.
func (h *Handler) Deactivate(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	orgUUID := chi.URLParam(r, "orgUUID")
	if err := h.repo.Deactivate(r.Context(), orgUUID); err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, errResponse{Error: "not_found"})
			return
		}
		h.log.Error("deactivate organization", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
