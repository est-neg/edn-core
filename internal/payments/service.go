package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/google/uuid"
	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/idempotency"
	commercialplans "github.com/villenneve/vil-core/internal/plans"
	"github.com/villenneve/vil-core/internal/platform/config"
)

const (
	checkoutCreateOperation   = "checkout.create"
	checkoutResourceTypeOrder = "order"
	versionedPlanSource       = "versioned"
	checkoutIntentKeyTTL      = 30 * time.Minute
)

type resolvedTenantScope struct {
	organizationID string
	tenantID       string
	organization   string
	tenant         string
	channel        string
}

type resolvedCheckoutPlan struct {
	planID          string
	planVersion     int
	planSource      string
	name            string
	slug            string
	billingCycle    string
	priceCents      int64
	currency        string
	maxInstallments int
}

// CheckoutService handles plan lookup, order creation, and InfinitePay checkout creation.
type CheckoutService struct {
	versionedPlans VersionedPlanRepository
	organizations  OrganizationRepository
	tenants        TenantRepository
	orders         OrderRepository
	idempotency    CheckoutIdempotencyRepository
	provider       InfinitePayClient
	lock           LockManager
	statusCache    StatusCache
	cfg            config.PaymentsConfig
	log            *zap.Logger
}

// NewCheckoutService constructs a CheckoutService with all required dependencies injected.
func NewCheckoutService(
	versionedPlans VersionedPlanRepository,
	organizations OrganizationRepository,
	tenants TenantRepository,
	orders OrderRepository,
	idempotency CheckoutIdempotencyRepository,
	provider InfinitePayClient,
	lock LockManager,
	statusCache StatusCache,
	cfg config.PaymentsConfig,
	log *zap.Logger,
) *CheckoutService {
	return &CheckoutService{
		versionedPlans: versionedPlans,
		organizations:  organizations,
		tenants:        tenants,
		orders:         orders,
		idempotency:    idempotency,
		provider:       provider,
		lock:           lock,
		statusCache:    statusCache,
		cfg:            cfg,
		log:            log,
	}
}

// CreateSession validates the request, resolves plan pricing, creates an order,
// calls InfinitePay, and returns the checkout URL.
// Price and amount always come from the plan — never from the request.
// When checkout_intent_key is provided, create-or-resume semantics are applied:
// an active, non-expired order is returned with 200 instead of creating a new one.
func (s *CheckoutService) CreateSession(ctx context.Context, req CreateCheckoutRequest) (CreateCheckoutResponse, error) {
	idempotencyKey := normalizedCheckoutIdempotencyKey(req.IdempotencyKey)
	if idempotencyKey == "" {
		return CreateCheckoutResponse{}, fmt.Errorf("%w: missing Idempotency-Key header", ErrInvalidRequest)
	}

	// Resume path: checkout_intent_key provided → bypass plan/tenant resolution.
	if intentKey := strings.TrimSpace(req.CheckoutIntentKey); intentKey != "" {
		return s.resumeSession(ctx, intentKey)
	}

	if err := ValidateCreateCheckoutRequest(req); err != nil {
		return CreateCheckoutResponse{}, err
	}

	phone, _ := NormalizePhone(req.Customer.Phone)
	email := strings.ToLower(strings.TrimSpace(req.Customer.Email))
	cpf, _ := NormalizeCPF(req.Customer.Document)
	customerName := strings.TrimSpace(req.Customer.Name)
	now := time.Now().UTC()
	scope, err := resolveTenantScope(ctx, s.organizations, s.tenants, req.OrganizationSlug, req.TenantSlug, req.Channel)
	if err != nil {
		return CreateCheckoutResponse{}, err
	}
	requestHash := CalculateCheckoutRequestHash(scope.organization, scope.tenant, scope.channel, req.PlanSlug, req.BillingCycle, customerName, email, phone, cpf)

	plan, err := s.resolveCheckoutPlan(ctx, scope, req.PlanSlug, req.BillingCycle, now)
	if err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("find plan: %w", err)
	}

	orderNSU, replay, err := s.reserveOrReplayCheckout(ctx, scope, idempotencyKey, requestHash, now)
	if err != nil {
		return CreateCheckoutResponse{}, err
	}
	if replay != nil {
		return *replay, nil
	}

	// Generate backend-owned resume handle and expiry before persisting the order.
	intentKey := GenerateCheckoutIntentKey()
	expiresAt := now.Add(checkoutIntentKeyTTL)

	order := checkout.Order{
		OrderNSU:          orderNSU,
		OrganizationID:    scope.organizationID,
		TenantID:          scope.tenantID,
		PlanID:            plan.planID,
		PlanSlug:          plan.slug,
		PlanVersion:       plan.planVersion,
		PlanSource:        plan.planSource,
		BillingCycle:      plan.billingCycle,
		AmountCents:       plan.priceCents, // ALWAYS from plan, never from request
		Currency:          plan.currency,
		CustomerName:      customerName,
		CustomerEmail:     email,
		CustomerPhone:     phone,
		CustomerDocument:  cpf,
		Status:            string(OrderStatusCreated),
		Provider:          "infinitepay",
		CheckoutIntentKey: intentKey,
		ExpiresAt:         expiresAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.orders.Create(ctx, order); err != nil {
		if errors.Is(err, checkout.ErrDuplicateOrder) {
			return s.replayExistingSession(ctx, scope.tenantID, idempotencyKey, requestHash)
		}
		return CreateCheckoutResponse{}, fmt.Errorf("create order: %w", err)
	}

	// Acquire order lock before calling provider
	lockToken := uuid.New().String()
	if err := s.lock.AcquireOrderLock(ctx, orderNSU, lockToken); err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("acquire lock: %w", err)
	}
	defer s.lock.ReleaseOrderLock(ctx, orderNSU, lockToken) //nolint:errcheck

	// Persist the create-attempt marker before calling the provider.
	// This ensures recovery can detect partial attempts and avoid unsafe blind recreation.
	attemptedAt := time.Now().UTC()
	if err := s.orders.MarkProviderCreateAttempted(ctx, orderNSU, attemptedAt); err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("persist provider create attempt marker: %w", err)
	}

	providerReq := InfinitePayCheckoutRequest{
		Handle:           s.cfg.InfinitePay.Handle,
		OrderNSU:         orderNSU,
		PlanName:         plan.name,
		AmountCents:      plan.priceCents,
		Currency:         plan.currency,
		MaxInstallments:  plan.maxInstallments,
		CustomerName:     order.CustomerName,
		CustomerEmail:    email,
		CustomerPhone:    phone,
		CustomerDocument: cpf,
		WebhookURL:       s.cfg.WebhookURL,
		RedirectURL:      s.cfg.RedirectURL,
	}

	providerResp, err := s.provider.CreateCheckout(ctx, providerReq)
	if err != nil {
		// Order stays as "created" and the idempotency key stays pending.
		// Same-key retries can replay once provider URL is persisted or resume if the order was never created.
		s.log.Warn("provider checkout creation failed", zap.String("order_nsu", orderNSU), zap.Error(err))
		return CreateCheckoutResponse{}, fmt.Errorf("provider checkout: %w", err)
	}

	updatedAt := time.Now().UTC()
	commitErr := s.idempotency.Commit(
		ctx,
		scope.tenantID,
		checkoutCreateOperation,
		idempotencyKey,
		orderNSU,
		string(OrderStatusCheckoutCreated),
		providerResp.CheckoutURL,
		providerResp.InvoiceSlug,
		updatedAt,
	)
	persistErr := s.persistCheckoutSession(ctx, orderNSU, providerResp.CheckoutURL, providerResp.InvoiceSlug, OrderStatusCheckoutCreated, updatedAt)
	if commitErr != nil && persistErr != nil {
		// Nothing durable: provider URL is in neither idempotency snapshot nor order.
		s.log.Error("checkout persist failed: both idempotency commit and order persist failed",
			zap.String("order_nsu", orderNSU), zap.Error(commitErr))
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}
	if persistErr != nil {
		// Idempotency commit succeeded (URL captured) but order persist failed.
		// A same-key retry can replay from the idempotency snapshot and repair the order.
		s.log.Error("checkout persist failed: order not updated after successful provider and idempotency commit",
			zap.String("order_nsu", orderNSU), zap.Error(persistErr))
		return CreateCheckoutResponse{}, ErrCheckoutRecoveryRequired
	}
	if commitErr != nil {
		// Order persist succeeded but idempotency commit failed.
		// The provider URL is durable on the order. Log a warning only.
		s.log.Warn("failed to commit checkout idempotency snapshot", zap.String("order_nsu", orderNSU), zap.Error(commitErr))
	}

	s.statusCache.SetOrderStatus(ctx, orderNSU, string(OrderStatusCheckoutCreated)) //nolint:errcheck

	s.log.Info("checkout created",
		zap.String("order_nsu", orderNSU),
		zap.String("tenant_id", scope.tenantID),
		zap.String("metric", "checkout_created"),
	)

	return CreateCheckoutResponse{
		OrderNSU:          orderNSU,
		CheckoutIntentKey: intentKey,
		Status:            OrderStatusCheckoutCreated,
		CheckoutURL:       providerResp.CheckoutURL,
		ExpiresAt:         expiresAt,
	}, nil
}

// resumeSession handles the resume path when a checkout_intent_key is provided.
// It locates the order by the backend-issued handle, validates eligibility, and returns
// the canonical success payload with Resumed=true so the handler emits 200.
func (s *CheckoutService) resumeSession(ctx context.Context, intentKey string) (CreateCheckoutResponse, error) {
	order, err := s.orders.FindByIntentKey(ctx, intentKey)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			// Unknown key: not a valid backend-issued handle.
			s.log.Warn("resume attempted with unknown checkout_intent_key",
				zap.String("metric", "checkout_non_resumable"),
			)
			return CreateCheckoutResponse{}, ErrCheckoutNonResumable
		}
		return CreateCheckoutResponse{}, fmt.Errorf("find order by intent key: %w", err)
	}

	now := time.Now().UTC()

	// Backend-authoritative expiry check.
	if !order.ExpiresAt.IsZero() && now.After(order.ExpiresAt) {
		s.log.Info("resume rejected: checkout expired",
			zap.String("order_nsu", order.OrderNSU),
			zap.String("tenant_id", order.TenantID),
			zap.String("metric", "checkout_expired"),
		)
		return CreateCheckoutResponse{}, ErrCheckoutExpired
	}

	// Status eligibility matrix.
	status := OrderStatus(order.Status)
	switch status {
	case OrderStatusPaid, OrderStatusFailed, OrderStatusExpired, OrderStatusPendingReview:
		s.log.Info("resume rejected: checkout not resumable",
			zap.String("order_nsu", order.OrderNSU),
			zap.String("tenant_id", order.TenantID),
			zap.String("status", string(status)),
			zap.String("metric", "checkout_non_resumable"),
		)
		return CreateCheckoutResponse{}, ErrCheckoutNonResumable
	}

	// Order has no confirmed provider URL — attempt synchronous recovery.
	if strings.TrimSpace(order.ProviderCheckoutURL) == "" {
		return s.recoverCheckoutSession(ctx, order)
	}

	s.log.Info("checkout resumed",
		zap.String("order_nsu", order.OrderNSU),
		zap.String("tenant_id", order.TenantID),
		zap.String("metric", "checkout_resumed"),
	)

	// Normalize status for the canonical response.
	if status == OrderStatusCreated {
		status = OrderStatusCheckoutCreated
	}

	return CreateCheckoutResponse{
		OrderNSU:          order.OrderNSU,
		CheckoutIntentKey: order.CheckoutIntentKey,
		Status:            status,
		CheckoutURL:       order.ProviderCheckoutURL,
		ExpiresAt:         order.ExpiresAt,
		Resumed:           true,
	}, nil
}

// recoverCheckoutSession is called by resumeSession when the order has no provider_checkout_url.
// It acquires the order lock, re-reads the order, and either returns success if the URL
// appeared while waiting, fails with ErrProviderStateAmbiguous for genuinely ambiguous state,
// or synchronously creates the InfinitePay checkout for the same order and persists the result.
func (s *CheckoutService) recoverCheckoutSession(ctx context.Context, firstRead *checkout.Order) (CreateCheckoutResponse, error) {
	lockToken := uuid.New().String()
	if err := s.lock.AcquireOrderLock(ctx, firstRead.OrderNSU, lockToken); err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("acquire recovery lock: %w", err)
	}
	defer s.lock.ReleaseOrderLock(ctx, firstRead.OrderNSU, lockToken) //nolint:errcheck

	// Re-read under lock — a concurrent create may have already persisted the URL.
	order, err := s.orders.GetByNSU(ctx, firstRead.OrderNSU)
	if err != nil {
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}

	now := time.Now().UTC()

	// Re-check expiry and terminal status on the freshly-read order.
	if !order.ExpiresAt.IsZero() && now.After(order.ExpiresAt) {
		return CreateCheckoutResponse{}, ErrCheckoutExpired
	}
	status := OrderStatus(order.Status)
	switch status {
	case OrderStatusPaid, OrderStatusFailed, OrderStatusExpired, OrderStatusPendingReview:
		return CreateCheckoutResponse{}, ErrCheckoutNonResumable
	}

	// URL appeared while we were waiting for the lock.
	if strings.TrimSpace(order.ProviderCheckoutURL) != "" {
		if status == OrderStatusCreated {
			status = OrderStatusCheckoutCreated
		}
		return CreateCheckoutResponse{
			OrderNSU:          order.OrderNSU,
			CheckoutIntentKey: order.CheckoutIntentKey,
			Status:            status,
			CheckoutURL:       order.ProviderCheckoutURL,
			ExpiresAt:         order.ExpiresAt,
			Resumed:           true,
		}, nil
	}

	// invoice_slug present without URL means a partial provider response was received;
	// re-creating would risk a duplicate charge.
	if strings.TrimSpace(order.InvoiceSlug) != "" {
		s.log.Warn("recovery blocked: invoice_slug present without provider_checkout_url",
			zap.String("order_nsu", order.OrderNSU),
			zap.String("tenant_id", order.TenantID),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}

	// If a provider-create attempt was previously recorded but no URL was persisted,
	// we cannot safely issue a new provider request. Try to repair from the idempotency snapshot.
	if order.ProviderCreateAttemptedAt != nil {
		return s.recoverFromIdempotencySnapshot(ctx, order)
	}

	// Validate we can reconstruct the provider request from the persisted plan.
	if strings.TrimSpace(order.PlanID) == "" {
		s.log.Warn("recovery blocked: order missing plan_id",
			zap.String("order_nsu", order.OrderNSU),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}

	plan, err := s.versionedPlans.FindByUUID(ctx, order.PlanID)
	if err != nil {
		s.log.Warn("recovery blocked: plan lookup failed",
			zap.String("order_nsu", order.OrderNSU),
			zap.String("plan_id", order.PlanID),
			zap.Error(err),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}

	if plan.PriceCents != order.AmountCents {
		s.log.Warn("recovery blocked: plan price mismatch",
			zap.String("order_nsu", order.OrderNSU),
			zap.Int64("plan_price_cents", plan.PriceCents),
			zap.Int64("order_amount_cents", order.AmountCents),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}

	providerReq := InfinitePayCheckoutRequest{
		Handle:           s.cfg.InfinitePay.Handle,
		OrderNSU:         order.OrderNSU,
		PlanName:         plan.Name,
		AmountCents:      plan.PriceCents,
		Currency:         plan.Currency,
		MaxInstallments:  plan.MaxInstallments,
		CustomerName:     order.CustomerName,
		CustomerEmail:    order.CustomerEmail,
		CustomerPhone:    order.CustomerPhone,
		CustomerDocument: order.CustomerDocument,
		WebhookURL:       s.cfg.WebhookURL,
		RedirectURL:      s.cfg.RedirectURL,
	}

	providerResp, err := s.provider.CreateCheckout(ctx, providerReq)
	if err != nil {
		s.log.Warn("recovery: provider checkout creation failed",
			zap.String("order_nsu", order.OrderNSU),
			zap.Error(err),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}

	updatedAt := time.Now().UTC()
	if err := s.persistCheckoutSession(ctx, order.OrderNSU, providerResp.CheckoutURL, providerResp.InvoiceSlug, OrderStatusCheckoutCreated, updatedAt); err != nil {
		s.log.Error("recovery: persist failed after successful provider response",
			zap.String("order_nsu", order.OrderNSU),
			zap.Error(err),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}

	s.statusCache.SetOrderStatus(ctx, order.OrderNSU, string(OrderStatusCheckoutCreated)) //nolint:errcheck

	s.log.Info("checkout recovered",
		zap.String("order_nsu", order.OrderNSU),
		zap.String("tenant_id", order.TenantID),
		zap.Bool("checkout_recovered", true),
	)

	return CreateCheckoutResponse{
		OrderNSU:          order.OrderNSU,
		CheckoutIntentKey: order.CheckoutIntentKey,
		Status:            OrderStatusCheckoutCreated,
		CheckoutURL:       providerResp.CheckoutURL,
		ExpiresAt:         order.ExpiresAt,
		Resumed:           true,
	}, nil
}

// recoverFromIdempotencySnapshot attempts to repair an order whose provider-create was attempted
// but whose provider_checkout_url was never persisted. It looks up the idempotency
// snapshot by order_nsu (resource_id) and, if committed with a durable ResourceURL, repairs the
// order without a second provider call. Returns ErrProviderStateAmbiguous when the snapshot
// cannot confirm the URL.
func (s *CheckoutService) recoverFromIdempotencySnapshot(ctx context.Context, order *checkout.Order) (CreateCheckoutResponse, error) {
	record, err := s.idempotency.FindByTenantOpResourceID(ctx, order.TenantID, checkoutCreateOperation, order.OrderNSU)
	if err != nil {
		s.log.Warn("recovery: idempotency snapshot not found",
			zap.String("order_nsu", order.OrderNSU),
			zap.Error(err),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}
	if record.Status != idempotency.StatusCommitted || record.ResourceURL == "" {
		s.log.Warn("recovery: idempotency snapshot not committed or missing durable URL",
			zap.String("order_nsu", order.OrderNSU),
			zap.String("snapshot_status", record.Status),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}
	// Repair the order from the snapshot — no new provider call.
	updatedAt := time.Now().UTC()
	if err := s.persistCheckoutSession(ctx, order.OrderNSU, record.ResourceURL, record.ExternalRef, OrderStatusCheckoutCreated, updatedAt); err != nil {
		s.log.Error("recovery: persist from idempotency snapshot failed",
			zap.String("order_nsu", order.OrderNSU),
			zap.Error(err),
			zap.String("metric", "checkout_recovery_ambiguous"),
		)
		return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
	}
	s.statusCache.SetOrderStatus(ctx, order.OrderNSU, string(OrderStatusCheckoutCreated)) //nolint:errcheck
	s.log.Info("checkout recovered from idempotency snapshot",
		zap.String("order_nsu", order.OrderNSU),
		zap.String("tenant_id", order.TenantID),
		zap.Bool("checkout_recovered", true),
	)
	return CreateCheckoutResponse{
		OrderNSU:          order.OrderNSU,
		CheckoutIntentKey: order.CheckoutIntentKey,
		Status:            OrderStatusCheckoutCreated,
		CheckoutURL:       record.ResourceURL,
		ExpiresAt:         order.ExpiresAt,
		Resumed:           true,
	}, nil
}

func (s *CheckoutService) replayExistingSession(ctx context.Context, tenantID, idempotencyKey, requestHash string) (CreateCheckoutResponse, error) {
	existing, err := s.idempotency.FindByTenantOpKey(ctx, tenantID, checkoutCreateOperation, idempotencyKey)
	if err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("find existing checkout idempotency key: %w", err)
	}
	if existing.RequestHash != requestHash {
		return CreateCheckoutResponse{}, ErrCheckoutConflict
	}
	if existing.Status == idempotency.StatusFailed || existing.ResourceID == "" {
		return CreateCheckoutResponse{}, ErrCheckoutConflict
	}

	order, err := s.orders.GetByNSU(ctx, existing.ResourceID)
	if err != nil {
		if existing.Status == idempotency.StatusCommitted && existing.ResourceURL != "" {
			return createCheckoutResponseFromRecord(existing, time.Time{}), nil
		}
		if errors.Is(err, ErrOrderNotFound) && existing.Status == idempotency.StatusPending {
			return CreateCheckoutResponse{}, ErrCheckoutInProgress
		}
		return CreateCheckoutResponse{}, fmt.Errorf("get idempotent order: %w", err)
	}
	if strings.TrimSpace(order.ProviderCheckoutURL) == "" {
		if existing.ResourceURL == "" {
			if order.ProviderCreateAttemptedAt != nil {
				return CreateCheckoutResponse{}, ErrProviderStateAmbiguous
			}
			return CreateCheckoutResponse{}, ErrCheckoutInProgress
		}
		s.repairCheckoutOrder(ctx, order.OrderNSU, existing.ResourceURL, existing.ExternalRef, existing.ResourceStatus, existing.UpdatedAt)
		return createCheckoutResponseFromRecord(existing, order.CreatedAt), nil
	}

	status := OrderStatus(order.Status)
	if status == OrderStatusCreated {
		status = OrderStatusCheckoutCreated
	}

	expiresAt := order.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = order.CreatedAt.Add(checkoutIntentKeyTTL)
	}

	return CreateCheckoutResponse{
		OrderNSU:          order.OrderNSU,
		CheckoutIntentKey: order.CheckoutIntentKey,
		Status:            status,
		CheckoutURL:       order.ProviderCheckoutURL,
		ExpiresAt:         expiresAt,
	}, nil
}

func (s *CheckoutService) reserveOrReplayCheckout(ctx context.Context, scope resolvedTenantScope, idempotencyKey, requestHash string, now time.Time) (string, *CreateCheckoutResponse, error) {
	proposedOrderNSU := GenerateOrderNSU()
	err := s.idempotency.Reserve(ctx, idempotency.Key{
		Operation:      checkoutCreateOperation,
		OrganizationID: scope.organizationID,
		TenantID:       scope.tenantID,
		IdempotencyKey: idempotencyKey,
		RequestHash:    requestHash,
		ResourceType:   checkoutResourceTypeOrder,
		ResourceID:     proposedOrderNSU,
		Status:         idempotency.StatusPending,
		Expirable:      true,
		ExpiresAt:      now.Add(24 * time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err == nil {
		return proposedOrderNSU, nil, nil
	}
	if !errors.Is(err, idempotency.ErrDuplicate) {
		return "", nil, fmt.Errorf("reserve checkout idempotency key: %w", err)
	}

	existing, err := s.idempotency.FindByTenantOpKey(ctx, scope.tenantID, checkoutCreateOperation, idempotencyKey)
	if err != nil {
		return "", nil, fmt.Errorf("find existing checkout idempotency key: %w", err)
	}
	if existing.RequestHash != requestHash {
		return "", nil, ErrCheckoutConflict
	}
	if existing.Status == idempotency.StatusFailed || existing.ResourceID == "" {
		return "", nil, ErrCheckoutConflict
	}

	order, err := s.orders.GetByNSU(ctx, existing.ResourceID)
	if err == nil {
		if strings.TrimSpace(order.ProviderCheckoutURL) == "" {
			if existing.ResourceURL == "" {
				if order.ProviderCreateAttemptedAt != nil {
					return "", nil, ErrProviderStateAmbiguous
				}
				return "", nil, ErrCheckoutInProgress
			}
			s.repairCheckoutOrder(ctx, order.OrderNSU, existing.ResourceURL, existing.ExternalRef, existing.ResourceStatus, existing.UpdatedAt)
			replay := createCheckoutResponseFromRecord(existing, order.CreatedAt)
			replay.CheckoutIntentKey = order.CheckoutIntentKey
			return "", &replay, nil
		}
		expiresAt := order.ExpiresAt
		if expiresAt.IsZero() {
			expiresAt = order.CreatedAt.Add(checkoutIntentKeyTTL)
		}
		replay := CreateCheckoutResponse{
			OrderNSU:          order.OrderNSU,
			CheckoutIntentKey: order.CheckoutIntentKey,
			Status:            OrderStatusCheckoutCreated,
			CheckoutURL:       order.ProviderCheckoutURL,
			ExpiresAt:         expiresAt,
		}
		return "", &replay, nil
	}
	if existing.Status == idempotency.StatusCommitted && existing.ResourceURL != "" {
		replay := createCheckoutResponseFromRecord(existing, time.Time{})
		return "", &replay, nil
	}
	if errors.Is(err, ErrOrderNotFound) && existing.Status == idempotency.StatusPending {
		return existing.ResourceID, nil, nil
	}
	return "", nil, fmt.Errorf("get existing idempotent order: %w", err)
}

func normalizedCheckoutIdempotencyKey(raw string) string {
	return strings.TrimSpace(raw)
}

func (s *CheckoutService) persistCheckoutSession(ctx context.Context, orderNSU, checkoutURL, invoiceSlug string, status OrderStatus, updatedAt time.Time) error {
	var persistErr error
	if err := s.orders.UpdateProviderURL(ctx, orderNSU, checkoutURL, invoiceSlug, updatedAt); err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("persist checkout url: %w", err))
	}
	if err := s.orders.UpdateStatus(ctx, orderNSU, status, updatedAt); err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("persist checkout status: %w", err))
	}
	return persistErr
}

func (s *CheckoutService) repairCheckoutOrder(ctx context.Context, orderNSU, checkoutURL, invoiceSlug, resourceStatus string, updatedAt time.Time) {
	status := OrderStatusCheckoutCreated
	if resourceStatus != "" {
		status = OrderStatus(resourceStatus)
	}
	if err := s.persistCheckoutSession(ctx, orderNSU, checkoutURL, invoiceSlug, status, updatedAt); err != nil {
		s.log.Warn("best-effort checkout order repair failed", zap.String("order_nsu", orderNSU), zap.Error(err))
	}
}

func createCheckoutResponseFromRecord(record *idempotency.Key, createdAt time.Time) CreateCheckoutResponse {
	expiresAtBase := createdAt
	if expiresAtBase.IsZero() {
		expiresAtBase = record.UpdatedAt
	}
	status := OrderStatusCheckoutCreated
	if record.ResourceStatus != "" {
		status = OrderStatus(record.ResourceStatus)
	}
	return CreateCheckoutResponse{
		OrderNSU:    record.ResourceID,
		Status:      status,
		CheckoutURL: record.ResourceURL,
		ExpiresAt:   expiresAtBase.Add(checkoutIntentKeyTTL),
		// CheckoutIntentKey is not stored in the idempotency record;
		// callers must set it from the order when available.
	}
}

// PlanQueryService handles GET /v1/plans.
type PlanQueryService struct {
	versionedPlans VersionedPlanRepository
	organizations  OrganizationRepository
	tenants        TenantRepository
	log            *zap.Logger
}

func NewPlanQueryService(versionedPlans VersionedPlanRepository, organizations OrganizationRepository, tenants TenantRepository, log *zap.Logger) *PlanQueryService {
	return &PlanQueryService{
		versionedPlans: versionedPlans,
		organizations:  organizations,
		tenants:        tenants,
		log:            log,
	}
}

func (s *PlanQueryService) ListActivePlans(ctx context.Context, query PlanListQuery) ([]PlanResponse, error) {
	scope, err := resolveTenantScope(ctx, s.organizations, s.tenants, query.OrganizationSlug, query.TenantSlug, query.Channel)
	if err != nil {
		return nil, err
	}

	plans, err := s.versionedPlans.ListActiveByTenant(ctx, scope.tenantID, scope.channel)
	if err != nil {
		return nil, fmt.Errorf("list active versioned plans: %w", err)
	}
	return versionedPlanResponses(plans), nil
}

// OrderStatusService handles GET /v1/orders/{orderNSU}/status.
type OrderStatusService struct {
	orders        OrderRepository
	subscriptions SubscriptionRepository
	cache         StatusCache
	log           *zap.Logger
}

func NewOrderStatusService(orders OrderRepository, subscriptions SubscriptionRepository, cache StatusCache, log *zap.Logger) *OrderStatusService {
	return &OrderStatusService{orders: orders, subscriptions: subscriptions, cache: cache, log: log}
}

func (s *OrderStatusService) GetStatus(ctx context.Context, orderNSU string) (OrderStatusResponse, error) {
	if err := ValidateOrderNSU(orderNSU); err != nil {
		return OrderStatusResponse{}, err
	}

	order, err := s.orders.GetByNSU(ctx, orderNSU)
	if err != nil {
		return OrderStatusResponse{}, fmt.Errorf("get order: %w", err)
	}

	resp := OrderStatusResponse{
		OrderNSU:   order.OrderNSU,
		Status:     OrderStatus(order.Status),
		PlanSlug:   order.PlanSlug,
		ReceiptURL: order.ReceiptURL,
	}

	sub, err := s.subscriptions.GetByOriginOrderNSU(ctx, orderNSU)
	if err == nil && sub != nil {
		resp.SubscriptionStatus = SubscriptionStatus(sub.Status)
	}

	return resp, nil
}

// TrackByIntentKey returns a public-safe TrackCheckoutResponse for the given checkout_intent_key.
// Returns ErrOrderNotFound when the key does not match any backend-issued handle.
// Does not return customer PII.
// Effective status semantics:
//   - If the order is non-terminal but expires_at has passed, status is returned as "expired" and resumable=false.
//   - checkout_url is only set when the order is still resumable/open.
func (s *OrderStatusService) TrackByIntentKey(ctx context.Context, intentKey string) (TrackCheckoutResponse, error) {
	order, err := s.orders.FindByIntentKey(ctx, intentKey)
	if err != nil {
		return TrackCheckoutResponse{}, err
	}

	now := time.Now().UTC()
	storedStatus := OrderStatus(order.Status)

	// If the order is non-terminal but the expiry window has passed, reflect expired semantics.
	effectivelyExpired := !order.ExpiresAt.IsZero() && now.After(order.ExpiresAt) && isResumableStatus(storedStatus)

	status := storedStatus
	if effectivelyExpired {
		status = OrderStatusExpired
	}

	resumable := isResumableStatus(storedStatus) && !effectivelyExpired

	resp := TrackCheckoutResponse{
		OrderNSU:          order.OrderNSU,
		CheckoutIntentKey: order.CheckoutIntentKey,
		Status:            status,
		PlanSlug:          order.PlanSlug,
		ExpiresAt:         order.ExpiresAt,
		UpdatedAt:         order.UpdatedAt,
		Resumable:         resumable,
	}
	if resumable && strings.TrimSpace(order.ProviderCheckoutURL) != "" {
		resp.CheckoutURL = order.ProviderCheckoutURL
	}
	if storedStatus == OrderStatusPaid {
		resp.ReceiptURL = order.ReceiptURL
	}
	return resp, nil
}

// RecoverByIntentKey exposes the resume+recovery path for the public tracking endpoint.
// It reuses the same eligibility checks and synchronous recovery logic as CreateSession.
// Returns ErrProviderStateAmbiguous when recovery is unsafe due to partial provider state.
func (s *CheckoutService) RecoverByIntentKey(ctx context.Context, intentKey string) (CreateCheckoutResponse, error) {
	return s.resumeSession(ctx, intentKey)
}

// SearchByDocument returns operational order summaries for a normalized CPF (11 digits).
// normalizedDocument must already be validated and normalized by the caller.
// The raw CPF is never logged inside this method.
func (s *OrderStatusService) SearchByDocument(ctx context.Context, normalizedDocument string) ([]AdminOrderSummary, error) {
	orders, err := s.orders.FindByCustomerDocument(ctx, normalizedDocument)
	if err != nil {
		return nil, fmt.Errorf("search orders by document: %w", err)
	}
	summaries := make([]AdminOrderSummary, 0, len(orders))
	for _, o := range orders {
		sum := AdminOrderSummary{
			OrderNSU:     o.OrderNSU,
			Status:       OrderStatus(o.Status),
			PlanSlug:     o.PlanSlug,
			BillingCycle: o.BillingCycle,
			AmountCents:  o.AmountCents,
			Currency:     o.Currency,
			CreatedAt:    o.CreatedAt,
			UpdatedAt:    o.UpdatedAt,
		}
		if !o.ExpiresAt.IsZero() {
			t := o.ExpiresAt
			sum.ExpiresAt = &t
		}
		summaries = append(summaries, sum)
	}
	return summaries, nil
}

// isResumableStatus reports whether the given order status allows a new checkout attempt.
func isResumableStatus(s OrderStatus) bool {
	switch s {
	case OrderStatusCreated, OrderStatusCheckoutCreated, OrderStatusPending:
		return true
	}
	return false
}

func (s *CheckoutService) resolveCheckoutPlan(ctx context.Context, scope resolvedTenantScope, planSlug, billingCycle string, now time.Time) (resolvedCheckoutPlan, error) {
	plan, err := s.versionedPlans.FindSellableByTenantSlug(ctx, scope.tenantID, planSlug, billingCycle, scope.channel, now)
	if err != nil {
		return resolvedCheckoutPlan{}, ErrPlanNotFound
	}
	return resolvedCheckoutPlan{
		planID:          plan.PlanUUID,
		planVersion:     plan.Version,
		planSource:      versionedPlanSource,
		name:            plan.Name,
		slug:            plan.Slug,
		billingCycle:    plan.BillingCycle,
		priceCents:      plan.PriceCents,
		currency:        plan.Currency,
		maxInstallments: plan.MaxInstallments,
	}, nil
}

func resolveTenantScope(ctx context.Context, organizations OrganizationRepository, tenants TenantRepository, organizationSlug, tenantSlug, channel string) (resolvedTenantScope, error) {
	normalizedChannel, err := NormalizeSalesChannel(channel)
	if err != nil {
		return resolvedTenantScope{}, err
	}
	if err := ValidateTenantScope(organizationSlug, tenantSlug); err != nil {
		return resolvedTenantScope{}, err
	}
	organizationSlug = strings.ToLower(strings.TrimSpace(organizationSlug))
	tenantSlug = strings.ToLower(strings.TrimSpace(tenantSlug))

	org, err := organizations.FindBySlug(ctx, organizationSlug)
	if err != nil || org == nil || !org.Active {
		return resolvedTenantScope{}, ErrOrganizationNotFound
	}
	tenant, err := tenants.FindByOrgAndSlug(ctx, org.OrgUUID, tenantSlug)
	if err != nil || tenant == nil || !tenant.Active {
		return resolvedTenantScope{}, ErrTenantNotFound
	}
	return resolvedTenantScope{
		organizationID: org.OrgUUID,
		tenantID:       tenant.TenantUUID,
		organization:   org.Slug,
		tenant:         tenant.Slug,
		channel:        normalizedChannel,
	}, nil
}

func versionedPlanResponses(plans []commercialplans.Plan) []PlanResponse {
	seen := make(map[string]struct{}, len(plans))
	resp := make([]PlanResponse, 0, len(plans))
	for _, plan := range plans {
		key := plan.Slug + "|" + plan.BillingCycle
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		resp = append(resp, PlanResponse{
			ID:              plan.PlanUUID,
			Slug:            plan.Slug,
			Name:            plan.Name,
			BillingCycle:    plan.BillingCycle,
			PriceCents:      plan.PriceCents,
			Currency:        plan.Currency,
			Active:          plan.Active,
			MaxInstallments: plan.MaxInstallments,
		})
	}
	return resp
}
