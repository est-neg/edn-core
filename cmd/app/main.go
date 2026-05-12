// Package main is the public HTTP entrypoint for the EDN Core backend.
// It serves leads and payments APIs without starting background workers.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/docs"
	"github.com/villenneve/vil-core/internal/leads"
	"github.com/villenneve/vil-core/internal/organizations"
	"github.com/villenneve/vil-core/internal/payments"
	commercialplans "github.com/villenneve/vil-core/internal/plans"
	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/platform/health"
	"github.com/villenneve/vil-core/internal/platform/logger"
	"github.com/villenneve/vil-core/internal/platform/mongodb"
	vilredis "github.com/villenneve/vil-core/internal/platform/redis"
	"github.com/villenneve/vil-core/internal/tenants"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		os.Stderr.WriteString("config load error: " + err.Error() + "\n")
		os.Exit(1)
	}

	log, err := logger.New(cfg.Log)
	if err != nil {
		os.Stderr.WriteString("logger init error: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer log.Sync() //nolint:errcheck

	// ── Required config validation ──────────────────────────────────────────
	if cfg.MongoDB.URI == "" {
		log.Fatal("mongodb uri is required")
	}
	if cfg.Leads.AuthToken == "" {
		log.Fatal("leads auth token is required")
	}
	if cfg.Payments.InfinitePay.APIToken == "" {
		log.Fatal("payments.infinitepay.api_token is required")
	}
	if cfg.Payments.InfinitePay.BaseURL == "" {
		log.Fatal("payments.infinitepay.base_url is required")
	}
	if cfg.Payments.InfinitePay.Handle == "" {
		log.Fatal("payments.infinitepay.handle is required")
	}
	if cfg.Redis.Addr == "" {
		log.Fatal("redis.addr is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Infrastructure ──────────────────────────────────────────────────────
	mongoClient, err := mongodb.New(ctx, cfg.MongoDB)
	if err != nil {
		log.Fatal("mongodb connect error", zap.Error(err))
	}
	defer mongoClient.Disconnect(context.Background()) //nolint:errcheck

	if err := mongodb.BootstrapLeadsStorage(ctx, mongoClient, cfg.MongoDB); err != nil {
		log.Fatal("leads mongodb bootstrap error", zap.Error(err))
	}
	if err := mongodb.BootstrapCheckoutStorage(ctx, mongoClient, cfg.MongoDB); err != nil {
		log.Fatal("checkout mongodb bootstrap error", zap.Error(err))
	}
	if err := mongodb.BootstrapTenancyStorage(ctx, mongoClient, cfg.MongoDB); err != nil {
		log.Fatal("tenancy mongodb bootstrap error", zap.Error(err))
	}

	health.SetReadinessCheck(func(ctx context.Context) error {
		return mongoClient.Ping(ctx, nil)
	})

	redisClient, err := vilredis.New(ctx, cfg.Redis)
	if err != nil {
		log.Fatal("redis connect error", zap.Error(err))
	}
	defer redisClient.Close()

	// ── Leads module ────────────────────────────────────────────────────────
	leadsRepo := leads.NewMongoRepository(mongoClient, cfg.MongoDB)
	leadNotifier := leads.Notifier(leads.NoopNotifier{})
	if cfg.Leads.NotificationWebhookURL != "" {
		leadNotifier = leads.NewWebhookNotifier(cfg.Leads.NotificationWebhookURL)
	}
	leadsService := leads.NewService(leadsRepo, leadNotifier, cfg.Leads, log)
	leadsHandler := leads.NewHandler(leadsService, cfg.Leads, log)
	protectedLeads := leads.NewProtectionMiddleware(cfg.Leads, log)(http.HandlerFunc(leadsHandler.ServeHTTP))

	// ── Payments module ─────────────────────────────────────────────────────
	checkoutStore := checkout.NewRedisStore(redisClient)
	storeAdapter := payments.NewCheckoutStoreAdapter(checkoutStore)

	versionedPlanRepo := commercialplans.NewMongoRepository(mongoClient, cfg.MongoDB)
	organizationRepo := organizations.NewMongoRepository(mongoClient, cfg.MongoDB)
	tenantRepo := tenants.NewMongoRepository(mongoClient, cfg.MongoDB)
	orderRepo := payments.NewMongoOrderRepository(mongoClient, cfg.MongoDB)
	checkoutIdempotencyRepo := payments.NewMongoCheckoutIdempotencyRepository(mongoClient, cfg.MongoDB)
	paymentRepo := payments.NewMongoPaymentRepository(mongoClient, cfg.MongoDB)
	subRepo := payments.NewMongoSubscriptionRepository(mongoClient, cfg.MongoDB)
	webhookRepo := payments.NewMongoWebhookEventRepository(mongoClient, cfg.MongoDB)
	outboxRepo := payments.NewMongoOutboxEventRepository(mongoClient, cfg.MongoDB)

	ipClient := payments.NewInfinitePayAdapter(
		cfg.Payments.InfinitePay.BaseURL,
		cfg.Payments.InfinitePay.APIToken,
		30,
		log,
	)

	checkoutSvc := payments.NewCheckoutService(versionedPlanRepo, organizationRepo, tenantRepo, orderRepo, checkoutIdempotencyRepo, ipClient, storeAdapter, storeAdapter, cfg.Payments, log)
	statusSvc := payments.NewOrderStatusService(orderRepo, subRepo, storeAdapter, log)
	planSvc := payments.NewPlanQueryService(versionedPlanRepo, organizationRepo, tenantRepo, log)
	webhookSvc := payments.NewWebhookService(
		orderRepo, paymentRepo, subRepo, webhookRepo, outboxRepo,
		storeAdapter, storeAdapter, storeAdapter,
		ipClient, log,
	)
	paymentsHandler := payments.NewHandler(checkoutSvc, statusSvc, planSvc, webhookSvc, cfg.HTTP.AdminAuthToken, "", log)

	// ── Router ──────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	if cfg.HTTP.CORSAllowedOrigins != "" {
		r.Use(corsMiddleware(cfg.HTTP.CORSAllowedOrigins))
	}

	// Health
	r.Get("/livez", health.LiveHandler)
	r.Get("/readyz", health.ReadyHandler)

	// API Docs — Scalar served at /docs, spec at /openapi.yaml
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", docs.ScalarCSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(docs.ScalarHTML) //nolint:errcheck
	})
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(docs.OpenAPISpec) //nolint:errcheck
	})

	// Leads
	r.Method(http.MethodPost, "/api/leads", protectedLeads)

	// Payments
	trackLimiter := payments.NewTrackRateLimiter(log)
	adminSearchLimiter := payments.NewAdminSearchRateLimiter(log)
	createSessionLimiter := payments.NewCreateSessionRateLimiter(log)

	r.Get("/v1/plans", paymentsHandler.ListPlans)
	r.Method(http.MethodPost, "/v1/checkout/sessions", createSessionLimiter(http.HandlerFunc(paymentsHandler.CreateCheckoutSession)))
	r.Method(http.MethodPost, "/v1/checkout/sessions/track", trackLimiter(http.HandlerFunc(paymentsHandler.TrackCheckoutSession)))
	r.Get("/v1/orders/{orderNSU}/status", paymentsHandler.GetOrderStatus)
	r.Method(http.MethodPost, "/api/v1/orders/search", adminSearchLimiter(http.HandlerFunc(paymentsHandler.SearchOrdersByDocument)))
	// NOTE: The public InfinitePay webhook route has been retired from this surface.
	// Public provider webhook ingress now belongs exclusively to cmd/webhook-relay (edn-webhook-dev).
	// The internal reconcile route is mounted only on the authenticated payments-api surface (cmd/payments-api).

	// ── HTTP server ─────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      r,
		ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.HTTP.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.HTTP.IdleTimeout) * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("server started",
			zap.String("addr", cfg.HTTP.Addr),
			zap.String("docs", "http://localhost"+cfg.HTTP.Addr+"/docs"),
		)
		serverErrors <- srv.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	case sig := <-shutdown:
		log.Info("shutdown signal received", zap.String("signal", sig.String()))
		cancel() // stop background workers
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown error", zap.Error(err))
		}
	}

	log.Info("app stopped")
}

// corsMiddleware returns a middleware that sets CORS headers for listed allowed origins.
// Origins are parsed from a comma-separated string (e.g. VIL_HTTP_CORS_ALLOWED_ORIGINS).
// Preflight OPTIONS requests are absorbed and answered without hitting downstream handlers.
// The webhook path is not affected because InfinitePay never sends an Origin header.
func corsMiddleware(allowedRaw string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{})
	for _, o := range strings.Split(allowedRaw, ",") {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Credentials", "false")
				}
			}
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
