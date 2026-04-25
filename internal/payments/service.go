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
	legacyCheckoutOrganizationID = "legacy-public"
	legacyCheckoutTenantID       = "legacy-public"
	checkoutCreateOperation      = "checkout.create"
	checkoutResourceTypeOrder    = "order"
	legacyPlanSource             = "legacy"
	versionedPlanSource          = "versioned"
)

type resolvedTenantScope struct {
	organizationID string
	tenantID       string
	organization   string
	tenant         string
	channel        string
	tenantAware    bool
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
	legacyPlans    PlanRepository
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
	legacyPlans PlanRepository,
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
		legacyPlans:    legacyPlans,
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
func (s *CheckoutService) CreateSession(ctx context.Context, req CreateCheckoutRequest) (CreateCheckoutResponse, error) {
	if err := ValidateCreateCheckoutRequest(req); err != nil {
		return CreateCheckoutResponse{}, err
	}
	idempotencyKey := normalizedCheckoutIdempotencyKey(req.IdempotencyKey)
	if idempotencyKey == "" {
		return CreateCheckoutResponse{}, fmt.Errorf("%w: missing Idempotency-Key header", ErrInvalidRequest)
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

	order := checkout.Order{
		OrderNSU:         orderNSU,
		OrganizationID:   scope.organizationID,
		TenantID:         scope.tenantID,
		PlanID:           plan.planID,
		PlanSlug:         plan.slug,
		PlanVersion:      plan.planVersion,
		PlanSource:       plan.planSource,
		BillingCycle:     plan.billingCycle,
		AmountCents:      plan.priceCents, // ALWAYS from plan, never from request
		Currency:         plan.currency,
		CustomerName:     customerName,
		CustomerEmail:    email,
		CustomerPhone:    phone,
		CustomerDocument: cpf,
		Status:           string(OrderStatusCreated),
		Provider:         "infinitepay",
		CreatedAt:        now,
		UpdatedAt:        now,
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

	providerReq := InfinitePayCheckoutRequest{
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
		return CreateCheckoutResponse{}, fmt.Errorf("persist checkout result: %w", errors.Join(commitErr, persistErr))
	}
	if commitErr != nil {
		s.log.Warn("failed to commit checkout idempotency snapshot", zap.String("order_nsu", orderNSU), zap.Error(commitErr))
	}
	if persistErr != nil {
		s.log.Error("failed to persist checkout order after successful provider response", zap.String("order_nsu", orderNSU), zap.Error(persistErr))
	}

	s.statusCache.SetOrderStatus(ctx, orderNSU, string(OrderStatusCheckoutCreated)) //nolint:errcheck

	return CreateCheckoutResponse{
		OrderNSU:    orderNSU,
		Status:      OrderStatusCheckoutCreated,
		CheckoutURL: providerResp.CheckoutURL,
		ExpiresAt:   now.Add(30 * time.Minute),
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
			return CreateCheckoutResponse{}, ErrCheckoutInProgress
		}
		s.repairCheckoutOrder(ctx, order.OrderNSU, existing.ResourceURL, existing.ExternalRef, existing.ResourceStatus, existing.UpdatedAt)
		return createCheckoutResponseFromRecord(existing, order.CreatedAt), nil
	}

	status := OrderStatus(order.Status)
	if status == OrderStatusCreated {
		status = OrderStatusCheckoutCreated
	}

	return CreateCheckoutResponse{
		OrderNSU:    order.OrderNSU,
		Status:      status,
		CheckoutURL: order.ProviderCheckoutURL,
		ExpiresAt:   order.CreatedAt.Add(30 * time.Minute),
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
				return "", nil, ErrCheckoutInProgress
			}
			s.repairCheckoutOrder(ctx, order.OrderNSU, existing.ResourceURL, existing.ExternalRef, existing.ResourceStatus, existing.UpdatedAt)
			replay := createCheckoutResponseFromRecord(existing, order.CreatedAt)
			return "", &replay, nil
		}
		replay := CreateCheckoutResponse{
			OrderNSU:    order.OrderNSU,
			Status:      OrderStatusCheckoutCreated,
			CheckoutURL: order.ProviderCheckoutURL,
			ExpiresAt:   order.CreatedAt.Add(30 * time.Minute),
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
		ExpiresAt:   expiresAtBase.Add(30 * time.Minute),
	}
}

// PlanQueryService handles GET /v1/plans.
type PlanQueryService struct {
	legacyPlans    PlanRepository
	versionedPlans VersionedPlanRepository
	organizations  OrganizationRepository
	tenants        TenantRepository
	log            *zap.Logger
}

func NewPlanQueryService(legacyPlans PlanRepository, versionedPlans VersionedPlanRepository, organizations OrganizationRepository, tenants TenantRepository, log *zap.Logger) *PlanQueryService {
	return &PlanQueryService{
		legacyPlans:    legacyPlans,
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
	if !scope.tenantAware {
		plans, err := s.legacyPlans.ListActive(ctx)
		if err != nil {
			return nil, fmt.Errorf("list active plans: %w", err)
		}
		resp := make([]PlanResponse, 0, len(plans))
		for _, p := range plans {
			resp = append(resp, PlanResponse{
				Slug:         p.Slug,
				Name:         p.Name,
				BillingCycle: p.BillingCycle,
				PriceCents:   p.PriceCents,
				Currency:     p.Currency,
			})
		}
		return resp, nil
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

func (s *CheckoutService) resolveCheckoutPlan(ctx context.Context, scope resolvedTenantScope, planSlug, billingCycle string, now time.Time) (resolvedCheckoutPlan, error) {
	if !scope.tenantAware {
		plan, err := s.legacyPlans.FindActiveBySlugAndCycle(ctx, planSlug, billingCycle)
		if err != nil {
			return resolvedCheckoutPlan{}, ErrPlanNotFound
		}
		return resolvedCheckoutPlan{
			planID:          plan.PlanID,
			planSource:      legacyPlanSource,
			name:            plan.Name,
			slug:            plan.Slug,
			billingCycle:    plan.BillingCycle,
			priceCents:      plan.PriceCents,
			currency:        plan.Currency,
			maxInstallments: plan.MaxInstallments,
		}, nil
	}

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
	if organizationSlug == "" && tenantSlug == "" {
		return resolvedTenantScope{
			organizationID: legacyCheckoutOrganizationID,
			tenantID:       legacyCheckoutTenantID,
			channel:        normalizedChannel,
		}, nil
	}

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
		tenantAware:    true,
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
			Slug:         plan.Slug,
			Name:         plan.Name,
			BillingCycle: plan.BillingCycle,
			PriceCents:   plan.PriceCents,
			Currency:     plan.Currency,
		})
	}
	return resp
}
