package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/checkout"
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

	if cfg.MongoDB.URI == "" {
		log.Fatal("mongodb uri is required")
	}
	if cfg.Redis.Addr == "" {
		log.Fatal("redis.addr is required")
	}
	if cfg.PubSub.ProjectID == "" {
		log.Fatal("pubsub.project_id is required")
	}
	if cfg.PubSub.PaymentApprovedTopic == "" {
		log.Fatal("pubsub.payment_approved_topic is required")
	}
	if cfg.PubSub.PaymentApprovedSubscription == "" {
		log.Fatal("pubsub.payment_approved_subscription is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mongoClient, err := mongodb.New(ctx, cfg.MongoDB)
	if err != nil {
		log.Fatal("mongodb connect error", zap.Error(err))
	}
	defer mongoClient.Disconnect(context.Background()) //nolint:errcheck
	if err := mongodb.BootstrapCheckoutStorage(ctx, mongoClient, cfg.MongoDB); err != nil {
		log.Fatal("checkout mongodb bootstrap error", zap.Error(err))
	}

	health.SetReadinessCheck(func(ctx context.Context) error {
		return mongoClient.Ping(ctx, nil)
	})
	defer health.ClearReadinessCheck()

	redisClient, err := vilredis.New(ctx, cfg.Redis)
	if err != nil {
		log.Fatal("redis connect error", zap.Error(err))
	}
	defer redisClient.Close()

	pubsubClient, err := pubsub.NewClient(ctx, cfg.PubSub.ProjectID)
	if err != nil {
		log.Fatal("pubsub client error", zap.Error(err))
	}
	defer pubsubClient.Close()
	publisher, err := payments.NewPubSubPublisher(pubsubClient, cfg.PubSub.PaymentApprovedTopic, log)
	if err != nil {
		log.Fatal("pubsub publisher error", zap.Error(err))
	}

	checkoutStore := checkout.NewRedisStore(redisClient)
	storeAdapter := payments.NewCheckoutStoreAdapter(checkoutStore)

	orderRepo := payments.NewMongoOrderRepository(mongoClient, cfg.MongoDB)
	subRepo := payments.NewMongoSubscriptionRepository(mongoClient, cfg.MongoDB)
	outboxRepo := payments.NewMongoOutboxEventRepository(mongoClient, cfg.MongoDB)

	activationSvc := payments.NewActivationService(orderRepo, subRepo, storeAdapter, log)
	dispatcher := payments.NewOutboxDispatcher(outboxRepo, publisher, log)

	sub := pubsubClient.Subscription(cfg.PubSub.PaymentApprovedSubscription)
	sub.ReceiveSettings.NumGoroutines = 1
	sub.ReceiveSettings.MaxOutstandingMessages = 10

	mux := http.NewServeMux()
	mux.HandleFunc("/livez", health.LiveHandler)
	mux.HandleFunc("/readyz", health.ReadyHandler)

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.HTTP.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.HTTP.IdleTimeout) * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("subscription-worker http server started",
			zap.String("addr", cfg.HTTP.Addr),
			zap.String("subscription", cfg.PubSub.PaymentApprovedSubscription),
		)
		serverErrors <- srv.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	workerErr := make(chan error, 1)
	go func() {
		workerErr <- sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
			var event payments.PaymentApprovedEvent
			if err := json.Unmarshal(msg.Data, &event); err != nil {
				log.Error("invalid payment approved event payload", zap.Error(err), zap.String("message_id", msg.ID))
				msg.Nack()
				return
			}
			if err := activationSvc.Activate(ctx, event); err != nil {
				log.Error("activation failed", zap.Error(err), zap.String("order_nsu", event.OrderNSU))
				msg.Nack()
				return
			}
			msg.Ack()
		})
	}()

	go func() {
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

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("worker http server error", zap.Error(err))
		}
		cancel()
	case sig := <-shutdown:
		log.Info("shutdown signal", zap.String("signal", sig.String()))
		cancel()
	case err := <-workerErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("worker receive error", zap.Error(err))
		}
		cancel()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("worker graceful shutdown error", zap.Error(err))
	}

	log.Info("subscription-worker stopped")
}
