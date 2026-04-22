package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
	"github.com/villenneve/vil-core/internal/payments"
	"github.com/villenneve/vil-core/internal/platform/config"
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

	// --- Required secret validation ---
	if cfg.MongoDB.URI == "" {
		log.Fatal("mongodb uri is required")
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
	if cfg.PubSub.ProjectID == "" {
		log.Fatal("pubsub.project_id is required")
	}

	ctx := context.Background()

	// --- MongoDB ---
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

	// --- Redis ---
	redisClient, err := vilredis.New(ctx, cfg.Redis)
	if err != nil {
		log.Fatal("redis connect error", zap.Error(err))
	}
	defer redisClient.Close()

	// --- Pub/Sub ---
	pubsubClient, err := pubsub.NewClient(ctx, cfg.PubSub.ProjectID)
	if err != nil {
		log.Fatal("pubsub client error", zap.Error(err))
	}
	defer pubsubClient.Close()

	// --- Adapters ---
	checkoutStore := checkout.NewRedisStore(redisClient)
	storeAdapter := payments.NewCheckoutStoreAdapter(checkoutStore)

	planRepo := payments.NewMongoPlanRepository(mongoClient, cfg.MongoDB)
	orderRepo := payments.NewMongoOrderRepository(mongoClient, cfg.MongoDB)
	paymentRepo := payments.NewMongoPaymentRepository(mongoClient, cfg.MongoDB)
	subRepo := payments.NewMongoSubscriptionRepository(mongoClient, cfg.MongoDB)
	webhookRepo := payments.NewMongoWebhookEventRepository(mongoClient, cfg.MongoDB)
	outboxRepo := payments.NewMongoOutboxEventRepository(mongoClient, cfg.MongoDB)

	publisher, err := payments.NewPubSubPublisher(pubsubClient, cfg.PubSub.PaymentApprovedTopic, log)
	if err != nil {
		log.Fatal("pubsub publisher error", zap.Error(err))
	}

	infinitePayClient := payments.NewInfinitePayAdapter(
		cfg.Payments.InfinitePay.BaseURL,
		cfg.Payments.InfinitePay.APIToken,
		30,
		log,
	)

	// --- Services ---
	checkoutSvc := payments.NewCheckoutService(planRepo, orderRepo, infinitePayClient, storeAdapter, storeAdapter, cfg.Payments, log)
	statusSvc := payments.NewOrderStatusService(orderRepo, subRepo, storeAdapter, log)
	planSvc := payments.NewPlanQueryService(planRepo, log)
	webhookSvc := payments.NewWebhookService(orderRepo, paymentRepo, subRepo, webhookRepo, outboxRepo, storeAdapter, storeAdapter, storeAdapter, infinitePayClient, log)

	handler := payments.NewHandler(checkoutSvc, statusSvc, planSvc, webhookSvc, log)

	// --- Router ---
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/v1/plans", handler.ListPlans)
	r.Post("/v1/checkout/sessions", handler.CreateCheckoutSession)
	r.Get("/v1/orders/{orderNSU}/status", handler.GetOrderStatus)
	// Webhook route uses exact secret path — NOT a URL parameter
	r.Post("/v1/webhooks/infinitepay/"+cfg.Payments.WebhookSecretPath, handler.HandleWebhook)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	// --- Outbox dispatcher ---
	dispatchCtx, dispatchCancel := context.WithCancel(ctx)
	defer dispatchCancel()
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
			case <-dispatchCtx.Done():
				return
			case <-ticker.C:
				if err := dispatcher.Dispatch(dispatchCtx, 50); err != nil {
					log.Error("outbox dispatch error", zap.Error(err))
				}
			}
		}
	}()

	// --- HTTP server ---
	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      r,
		ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.HTTP.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.HTTP.IdleTimeout) * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() { serverErrors <- srv.ListenAndServe() }()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	case sig := <-shutdown:
		log.Info("shutdown signal received", zap.String("signal", sig.String()))
		dispatchCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown error", zap.Error(err))
		}
	}

	log.Info("payments-api stopped")
}
