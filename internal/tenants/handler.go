package tenants

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

// Handler handles HTTP requests for the tenants resource.
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

// ListByOrg handles GET /api/v1/organizations/{orgUUID}/tenants.
func (h *Handler) ListByOrg(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	orgUUID := chi.URLParam(r, "orgUUID")
	list, err := h.repo.ListByOrg(r.Context(), orgUUID)
	if err != nil {
		h.log.Error("list tenants", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusOK, list)
}

// createRequest is the request body for creating a tenant.
type createRequest struct {
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Channels []string `json:"channels"`
}

// Create handles POST /api/v1/organizations/{orgUUID}/tenants.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	orgUUID := chi.URLParam(r, "orgUUID")
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid_payload"})
		return
	}
	if req.Slug == "" || req.Name == "" {
		h.writeJSON(w, http.StatusBadRequest, errResponse{Error: "slug and name are required"})
		return
	}
	if len(req.Channels) == 0 {
		req.Channels = []string{"web"}
	}
	now := time.Now().UTC()
	tenant := Tenant{
		TenantUUID:     uuid.New().String(),
		OrganizationID: orgUUID,
		Slug:           req.Slug,
		Name:           req.Name,
		Channels:       req.Channels,
		Active:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := h.repo.Create(r.Context(), tenant); err != nil {
		if errors.Is(err, ErrDuplicate) {
			h.writeJSON(w, http.StatusConflict, errResponse{Error: "duplicate"})
			return
		}
		h.log.Error("create tenant", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusCreated, tenant)
}

// GetByUUID handles GET /api/v1/organizations/{orgUUID}/tenants/{tenantUUID}.
func (h *Handler) GetByUUID(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	tenantUUID := chi.URLParam(r, "tenantUUID")
	tenant, err := h.repo.FindByUUID(r.Context(), tenantUUID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, errResponse{Error: "not_found"})
			return
		}
		h.log.Error("find tenant", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusOK, tenant)
}

// updateRequest is the request body for updating a tenant.
type updateRequest struct {
	Name     string   `json:"name"`
	Channels []string `json:"channels"`
	Active   bool     `json:"active"`
}

// Update handles PUT /api/v1/organizations/{orgUUID}/tenants/{tenantUUID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	tenantUUID := chi.URLParam(r, "tenantUUID")
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid_payload"})
		return
	}
	patch := Tenant{
		Name:     req.Name,
		Channels: req.Channels,
		Active:   req.Active,
	}
	if err := h.repo.Update(r.Context(), tenantUUID, patch); err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, errResponse{Error: "not_found"})
			return
		}
		h.log.Error("update tenant", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// Deactivate handles DELETE /api/v1/organizations/{orgUUID}/tenants/{tenantUUID}.
func (h *Handler) Deactivate(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		h.writeJSON(w, http.StatusUnauthorized, errResponse{Error: "unauthorized"})
		return
	}
	tenantUUID := chi.URLParam(r, "tenantUUID")
	if err := h.repo.Deactivate(r.Context(), tenantUUID); err != nil {
		if errors.Is(err, ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, errResponse{Error: "not_found"})
			return
		}
		h.log.Error("deactivate tenant", zap.Error(err),
			zap.String("request_id", middleware.GetReqID(r.Context())))
		h.writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal_error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
