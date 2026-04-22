package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"cloud.google.com/go/pubsub"
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

	if cfg.MongoDB.URI == "" {
		log.Fatal("mongodb uri is required")
	}
	if cfg.Redis.Addr == "" {
		log.Fatal("redis.addr is required")
	}
	if cfg.PubSub.ProjectID == "" {
		log.Fatal("pubsub.project_id is required")
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

	checkoutStore := checkout.NewRedisStore(redisClient)
	storeAdapter := payments.NewCheckoutStoreAdapter(checkoutStore)

	planRepo := payments.NewMongoPlanRepository(mongoClient, cfg.MongoDB)
	orderRepo := payments.NewMongoOrderRepository(mongoClient, cfg.MongoDB)
	subRepo := payments.NewMongoSubscriptionRepository(mongoClient, cfg.MongoDB)

	activationSvc := payments.NewActivationService(orderRepo, subRepo, planRepo, storeAdapter, log)

	sub := pubsubClient.Subscription(cfg.PubSub.PaymentApprovedSubscription)

	log.Info("subscription-worker started", zap.String("subscription", cfg.PubSub.PaymentApprovedSubscription))

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

	select {
	case sig := <-shutdown:
		log.Info("shutdown signal", zap.String("signal", sig.String()))
		cancel()
	case err := <-workerErr:
		if err != nil {
			log.Error("worker receive error", zap.Error(err))
		}
	}

	log.Info("subscription-worker stopped")
}
