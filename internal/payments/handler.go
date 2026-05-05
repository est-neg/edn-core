package payments

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// Handler holds all HTTP handlers for the payments API.
type Handler struct {
	checkout  *CheckoutService
	status    *OrderStatusService
	plans     *PlanQueryService
	webhook   *WebhookService
	authToken string // full Authorization header value expected for admin endpoints
	log       *zap.Logger
}

// NewHandler constructs a Handler with all required services injected.
// adminAuthToken is the full expected Authorization header value for admin endpoints
// (e.g. "Bearer <token>"). Constant-time compared on each admin request.
func NewHandler(
	checkout *CheckoutService,
	status *OrderStatusService,
	plans *PlanQueryService,
	webhook *WebhookService,
	adminAuthToken string,
	log *zap.Logger,
) *Handler {
	return &Handler{checkout: checkout, status: status, plans: plans, webhook: webhook, authToken: adminAuthToken, log: log}
}

// authenticate performs constant-time comparison of the Authorization header.
func (h *Handler) authenticate(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	if h.authToken == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.authToken)) == 1
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
// Returns 201 for a newly created checkout and 200 for a resumed checkout.
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
		if errors.Is(err, ErrCheckoutConflict) {
			writeJSON(w, http.StatusConflict, CheckoutErrorResponse{
				Error:     "checkout idempotency conflict",
				ErrorCode: ErrCodeCheckoutIdempotencyConflict,
			})
			return
		}
		if errors.Is(err, ErrCheckoutInProgress) {
			writeJSON(w, http.StatusConflict, CheckoutErrorResponse{
				Error:     "checkout already in progress",
				ErrorCode: ErrCodeCheckoutInProgress,
			})
			return
		}
		if errors.Is(err, ErrCheckoutNonResumable) {
			writeJSON(w, http.StatusConflict, CheckoutErrorResponse{
				Error:     "checkout not resumable",
				ErrorCode: ErrCodeCheckoutNonResumable,
			})
			return
		}
		if errors.Is(err, ErrCheckoutExpired) {
			writeJSON(w, http.StatusConflict, CheckoutErrorResponse{
				Error:     "checkout expired",
				ErrorCode: ErrCodeCheckoutExpired,
			})
			return
		}
		if errors.Is(err, ErrCheckoutRecoveryRequired) {
			writeJSON(w, http.StatusServiceUnavailable, CheckoutErrorResponse{
				Error:     "checkout recovery required",
				ErrorCode: ErrCodeCheckoutRecoveryRequired,
			})
			return
		}
		if errors.Is(err, ErrProviderStateAmbiguous) {
			writeJSON(w, http.StatusServiceUnavailable, CheckoutErrorResponse{
				Error:     "provider state ambiguous",
				ErrorCode: ErrCodeProviderStateAmbiguous,
			})
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
	statusCode := http.StatusCreated
	if resp.Resumed {
		statusCode = http.StatusOK
	}
	writeJSON(w, statusCode, resp)
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

// TrackCheckoutSession handles POST /v1/checkout/sessions/track.
// Public endpoint — no auth required. The checkout_intent_key is the capability token.
// Returns coarse payment progress. Never returns customer PII.
func (h *Handler) TrackCheckoutSession(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var req TrackCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	intentKey := strings.TrimSpace(req.CheckoutIntentKey)
	if intentKey == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "checkout_intent_key is required"})
		return
	}
	resp, err := h.status.TrackByIntentKey(r.Context(), intentKey)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "checkout not found"})
			return
		}
		if errors.Is(err, ErrProviderStateAmbiguous) {
			writeJSON(w, http.StatusServiceUnavailable, CheckoutErrorResponse{
				Error:     "provider state ambiguous",
				ErrorCode: ErrCodeProviderStateAmbiguous,
			})
			return
		}
		h.log.Error("track checkout session failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	// Recovery path: order is open but has no provider URL — attempt synchronous recovery.
	// This handles the case where the original checkout call succeeded at the order layer
	// but failed before the provider URL was persisted.
	if resp.Resumable && resp.CheckoutURL == "" && h.checkout != nil {
		_, recErr := h.checkout.RecoverByIntentKey(r.Context(), intentKey)
		if recErr == nil {
			// Re-read the tracking state after successful persistence so the response
			// reflects the latest order state rather than the stale pre-recovery snapshot.
			fresh, freshErr := h.status.TrackByIntentKey(r.Context(), intentKey)
			if freshErr == nil {
				writeJSON(w, http.StatusOK, fresh)
				return
			}
			// Recovery succeeded but fresh reread failed — returning stale state would be
			// misleading. Signal a transient error so the client retries.
			h.log.Error("track: recovery succeeded but fresh reread failed",
				zap.Error(freshErr),
				zap.String("metric", "track_stale_fallback_blocked"),
			)
			writeJSON(w, http.StatusServiceUnavailable, CheckoutErrorResponse{
				Error:     "checkout recovery required",
				ErrorCode: ErrCodeCheckoutRecoveryRequired,
			})
			return
		} else if errors.Is(recErr, ErrProviderStateAmbiguous) {
			writeJSON(w, http.StatusServiceUnavailable, CheckoutErrorResponse{
				Error:     "provider state ambiguous",
				ErrorCode: ErrCodeProviderStateAmbiguous,
			})
			return
		} else {
			// Non-ambiguous recovery failure (e.g. expired, non-resumable under lock):
			// attempt one fresh read so we don't return stale state when the order changed under lock.
			if fresh, err := h.status.TrackByIntentKey(r.Context(), intentKey); err == nil {
				writeJSON(w, http.StatusOK, fresh)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// SearchOrdersByDocument handles POST /api/v1/orders/search.
// Admin-only endpoint — requires Authorization header with the configured admin token.
// Normalizes the CPF server-side. Never logs the raw CPF.
func (h *Handler) SearchOrdersByDocument(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var req AdminOrderSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if strings.TrimSpace(req.Document) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "document is required"})
		return
	}
	cpf, err := NormalizeCPF(req.Document)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid document: must be a valid CPF"})
		return
	}
	orders, err := h.status.SearchByDocument(r.Context(), cpf)
	if err != nil {
		h.log.Error("search orders by document failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	h.log.Info("admin CPF search executed",
		zap.Int("result_count", len(orders)),
		zap.String("metric", "admin_cpf_search"),
	)
	writeJSON(w, http.StatusOK, AdminOrderSearchResponse{Orders: orders})
}
