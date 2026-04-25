package payments

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// Handler holds all HTTP handlers for the payments API.
type Handler struct {
	checkout *CheckoutService
	status   *OrderStatusService
	plans    *PlanQueryService
	webhook  *WebhookService
	log      *zap.Logger
}

// NewHandler constructs a Handler with all required services injected.
func NewHandler(
	checkout *CheckoutService,
	status *OrderStatusService,
	plans *PlanQueryService,
	webhook *WebhookService,
	log *zap.Logger,
) *Handler {
	return &Handler{checkout: checkout, status: status, plans: plans, webhook: webhook, log: log}
}

// ListPlans handles GET /v1/plans.
func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := h.plans.ListActivePlans(r.Context(), PlanListQuery{
		OrganizationSlug: r.URL.Query().Get("organization_slug"),
		TenantSlug:       r.URL.Query().Get("tenant_slug"),
		Channel:          r.URL.Query().Get("channel"),
	})
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrOrganizationNotFound) || errors.Is(err, ErrTenantNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "organization or tenant not found"})
			return
		}
		h.log.Error("list plans failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"plans": plans})
}

// CreateCheckoutSession handles POST /v1/checkout/sessions.
func (h *Handler) CreateCheckoutSession(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req CreateCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	resp, err := h.checkout.CreateSession(r.Context(), req)
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrCheckoutConflict) || errors.Is(err, ErrCheckoutInProgress) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrPlanNotFound) || errors.Is(err, ErrPlanInactive) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "plan not found or inactive"})
			return
		}
		if errors.Is(err, ErrOrganizationNotFound) || errors.Is(err, ErrTenantNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "organization or tenant not found"})
			return
		}
		h.log.Error("create checkout session failed", zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "checkout unavailable"})
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// GetOrderStatus handles GET /v1/orders/{orderNSU}/status.
// orderNSU is extracted from the URL path by the router.
func (h *Handler) GetOrderStatus(w http.ResponseWriter, r *http.Request) {
	// Extract orderNSU from path: /v1/orders/{orderNSU}/status
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/orders/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing order_nsu"})
		return
	}
	orderNSU := parts[0]
	resp, err := h.status.GetStatus(r.Context(), orderNSU)
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, ErrOrderNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found"})
			return
		}
		h.log.Error("get order status failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleWebhook handles POST /v1/webhooks/infinitepay/{secretPath}.
// The router MUST only mount this at the exact configured secret path.
// This handler must NEVER log r.URL.Path (would leak the secret).
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "content-type must be application/json"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if len(rawBody) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
		return
	}

	err = h.webhook.Handle(r.Context(), rawBody)
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook payload"})
			return
		}
		// Log without request path to avoid leaking secret
		h.log.Error("webhook processing failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "processing error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
