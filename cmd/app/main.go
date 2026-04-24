// Package main is the unified entrypoint for the EDN Core backend.
// It starts the leads API, payments API, subscription worker, and outbox dispatcher
// in a single process — one command to run everything locally (like npm run dev).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/docs"
	"github.com/villenneve/vil-core/internal/leads"
	"github.com/villenneve/vil-core/internal/payments"
	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/platform/health"
	"github.com/villenneve/vil-core/internal/platform/logger"
	"github.com/villenneve/vil-core/internal/platform/mongodb"
	vilredis "github.com/villenneve/vil-core/internal/platform/redis"
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
	if cfg.Payments.WebhookSecretPath == "" {
		log.Fatal("payments.webhook_secret_path is required")
	}
	if cfg.Payments.InfinitePay.APIToken == "" {
		log.Fatal("payments.infinitepay.api_token is required")
	}
	if cfg.Payments.InfinitePay.BaseURL == "" {
		log.Fatal("payments.infinitepay.base_url is required")
	}
	if cfg.Redis.Addr == "" {
		log.Fatal("redis.addr is required")
	}
	// Pub/Sub is optional — omit VIL_PUBSUB_PROJECT_ID to disable locally.
	// When disabled, a noop publisher is used and the subscription worker does not start.

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

	health.SetReadinessCheck(func(ctx context.Context) error {
		return mongoClient.Ping(ctx, nil)
	})

	redisClient, err := vilredis.New(ctx, cfg.Redis)
	if err != nil {
		log.Fatal("redis connect error", zap.Error(err))
	}
	defer redisClient.Close()

	// ── Pub/Sub (optional — disabled when project_id is empty) ─────────────
	var publisher payments.PaymentEventPublisher
	var pubsubClient *pubsub.Client
	if cfg.PubSub.ProjectID != "" && cfg.PubSub.PaymentApprovedTopic == "" {
		log.Fatal("pubsub.payment_approved_topic is required when pubsub.project_id is set")
	}
	if cfg.PubSub.ProjectID != "" && cfg.PubSub.PaymentApprovedSubscription == "" {
		log.Fatal("pubsub.payment_approved_subscription is required when pubsub.project_id is set")
	}
	pubsubEnabled := cfg.PubSub.ProjectID != ""
	if pubsubEnabled {
		var err error
		pubsubClient, err = pubsub.NewClient(ctx, cfg.PubSub.ProjectID)
		if err != nil {
			log.Fatal("pubsub client error", zap.Error(err))
		}
		defer pubsubClient.Close()
		publisher, err = payments.NewPubSubPublisher(pubsubClient, cfg.PubSub.PaymentApprovedTopic, log)
		if err != nil {
			log.Fatal("pubsub publisher error", zap.Error(err))
		}
	} else {
		log.Warn("pubsub disabled (VIL_PUBSUB_PROJECT_ID not set) — using noop publisher")
		publisher = payments.NewNoopPublisher(log)
	}

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

	planRepo := payments.NewMongoPlanRepository(mongoClient, cfg.MongoDB)
	orderRepo := payments.NewMongoOrderRepository(mongoClient, cfg.MongoDB)
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

	checkoutSvc := payments.NewCheckoutService(planRepo, orderRepo, ipClient, storeAdapter, storeAdapter, cfg.Payments, log)
	statusSvc := payments.NewOrderStatusService(orderRepo, subRepo, storeAdapter, log)
	planSvc := payments.NewPlanQueryService(planRepo, log)
	webhookSvc := payments.NewWebhookService(
		orderRepo, paymentRepo, subRepo, webhookRepo, outboxRepo,
		storeAdapter, storeAdapter, storeAdapter,
		ipClient, log,
	)
	paymentsHandler := payments.NewHandler(checkoutSvc, statusSvc, planSvc, webhookSvc, log)

	// ── Subscription worker (background — only when Pub/Sub is enabled) ────
	if pubsubEnabled {
		activationSvc := payments.NewActivationService(orderRepo, subRepo, planRepo, storeAdapter, log)
		sub := pubsubClient.Subscription(cfg.PubSub.PaymentApprovedSubscription)
		go func() {
			log.Info("subscription worker started",
				zap.String("subscription", cfg.PubSub.PaymentApprovedSubscription),
			)
			if err := sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
				var event payments.PaymentApprovedEvent
				if err := json.Unmarshal(msg.Data, &event); err != nil {
					log.Error("invalid event payload", zap.Error(err), zap.String("message_id", msg.ID))
					msg.Nack()
					return
				}
				if err := activationSvc.Activate(ctx, event); err != nil {
					log.Error("activation failed", zap.Error(err), zap.String("order_nsu", event.OrderNSU))
					msg.Nack()
					return
				}
				msg.Ack()
			}); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("subscription worker error", zap.Error(err))
			}
		}()
	}

	// ── Outbox dispatcher (background) ─────────────────────────────────────
	go func() {
		dispatcher := payments.NewOutboxDispatcher(outboxRepo, publisher, log)
		interval := time.Duration(cfg.Payments.OutboxDispatchIntervalSec) * time.Second
		if interval == 0 {
			interval = 30 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := dispatcher.Dispatch(ctx, 50); err != nil {
					log.Error("outbox dispatch error", zap.Error(err))
				}
			}
		}
	}()

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
	r.Get("/v1/plans", paymentsHandler.ListPlans)
	r.Post("/v1/checkout/sessions", paymentsHandler.CreateCheckoutSession)
	r.Get("/v1/orders/{orderNSU}/status", paymentsHandler.GetOrderStatus)
	// Webhook route uses a secret path segment — NOT a URL parameter — to prevent enumeration.
	r.Post("/v1/webhooks/infinitepay/"+cfg.Payments.WebhookSecretPath, paymentsHandler.HandleWebhook)

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
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
