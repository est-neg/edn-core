package payments

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/google/uuid"
	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/platform/config"
)

// CheckoutService handles plan lookup, order creation, and InfinitePay checkout creation.
type CheckoutService struct {
	plans       PlanRepository
	orders      OrderRepository
	provider    InfinitePayClient
	lock        LockManager
	statusCache StatusCache
	cfg         config.PaymentsConfig
	log         *zap.Logger
}

// NewCheckoutService constructs a CheckoutService with all required dependencies injected.
func NewCheckoutService(
	plans PlanRepository,
	orders OrderRepository,
	provider InfinitePayClient,
	lock LockManager,
	statusCache StatusCache,
	cfg config.PaymentsConfig,
	log *zap.Logger,
) *CheckoutService {
	return &CheckoutService{
		plans:       plans,
		orders:      orders,
		provider:    provider,
		lock:        lock,
		statusCache: statusCache,
		cfg:         cfg,
		log:         log,
	}
}

// CreateSession validates the request, resolves plan pricing, creates an order,
// calls InfinitePay, and returns the checkout URL.
// Price and amount always come from the plan — never from the request.
func (s *CheckoutService) CreateSession(ctx context.Context, req CreateCheckoutRequest) (CreateCheckoutResponse, error) {
	if err := ValidateCreateCheckoutRequest(req); err != nil {
		return CreateCheckoutResponse{}, err
	}

	phone, _ := NormalizePhone(req.Customer.Phone)
	email := strings.ToLower(strings.TrimSpace(req.Customer.Email))

	plan, err := s.plans.FindActiveBySlugAndCycle(ctx, req.PlanSlug, req.BillingCycle)
	if err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("find plan: %w", err)
	}

	orderNSU := GenerateOrderNSU()
	now := time.Now().UTC()

	order := checkout.Order{
		OrderNSU:      orderNSU,
		PlanID:        plan.PlanID,
		PlanSlug:      plan.Slug,
		BillingCycle:  plan.BillingCycle,
		AmountCents:   plan.PriceCents, // ALWAYS from plan, never from request
		Currency:      plan.Currency,
		CustomerName:  strings.TrimSpace(req.Customer.Name),
		CustomerEmail: email,
		CustomerPhone: phone,
		Status:        string(OrderStatusCreated),
		Provider:      "infinitepay",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.orders.Create(ctx, order); err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("create order: %w", err)
	}

	// Acquire order lock before calling provider
	lockToken := uuid.New().String()
	if err := s.lock.AcquireOrderLock(ctx, orderNSU, lockToken); err != nil {
		return CreateCheckoutResponse{}, fmt.Errorf("acquire lock: %w", err)
	}
	defer s.lock.ReleaseOrderLock(ctx, orderNSU, lockToken) //nolint:errcheck

	providerReq := InfinitePayCheckoutRequest{
		OrderNSU:        orderNSU,
		PlanName:        plan.Name,
		AmountCents:     plan.PriceCents,
		Currency:        plan.Currency,
		MaxInstallments: plan.MaxInstallments,
		CustomerName:    order.CustomerName,
		CustomerEmail:   email,
		CustomerPhone:   phone,
		WebhookURL:      s.cfg.WebhookURL,
		RedirectURL:     s.cfg.RedirectURL,
	}

	providerResp, err := s.provider.CreateCheckout(ctx, providerReq)
	if err != nil {
		// Order stays as "created" — resumable on retry
		s.log.Warn("provider checkout creation failed", zap.String("order_nsu", orderNSU), zap.Error(err))
		return CreateCheckoutResponse{}, fmt.Errorf("provider checkout: %w", err)
	}

	if err := s.orders.UpdateProviderURL(ctx, orderNSU, providerResp.CheckoutURL, providerResp.InvoiceSlug, time.Now().UTC()); err != nil {
		s.log.Error("failed to update provider url after successful checkout", zap.String("order_nsu", orderNSU), zap.Error(err))
		return CreateCheckoutResponse{}, fmt.Errorf("persist checkout url: %w", err)
	}

	s.statusCache.SetOrderStatus(ctx, orderNSU, string(OrderStatusCheckoutCreated)) //nolint:errcheck

	return CreateCheckoutResponse{
		OrderNSU:    orderNSU,
		Status:      OrderStatusCheckoutCreated,
		CheckoutURL: providerResp.CheckoutURL,
		ExpiresAt:   now.Add(30 * time.Minute),
	}, nil
}

// PlanQueryService handles GET /v1/plans.
type PlanQueryService struct {
	plans PlanRepository
	log   *zap.Logger
}

func NewPlanQueryService(plans PlanRepository, log *zap.Logger) *PlanQueryService {
	return &PlanQueryService{plans: plans, log: log}
}

func (s *PlanQueryService) ListActivePlans(ctx context.Context) ([]PlanResponse, error) {
	plans, err := s.plans.ListActive(ctx)
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
