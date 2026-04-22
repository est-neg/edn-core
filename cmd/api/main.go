package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/leads"
	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/platform/health"
	"github.com/villenneve/vil-core/internal/platform/httpserver"
	"github.com/villenneve/vil-core/internal/platform/logger"
	"github.com/villenneve/vil-core/internal/platform/mongodb"
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
		log.Error("mongodb uri is required")
		os.Exit(1)
	}
	if cfg.MongoDB.Database == "" || cfg.MongoDB.CollectionLeads == "" {
		log.Error("mongodb database and leads collection are required")
		os.Exit(1)
	}
	if cfg.Leads.AuthToken == "" {
		log.Error("leads auth token is required")
		os.Exit(1)
	}

	// MongoDB bootstrap
	ctx := context.Background()
	mongoClient, err := mongodb.New(ctx, cfg.MongoDB)
	if err != nil {
		log.Error("mongodb connect error", zap.Error(err))
		os.Exit(1)
	}
	defer mongoClient.Disconnect(context.Background()) //nolint:errcheck

	if err := mongodb.BootstrapLeadsStorage(ctx, mongoClient, cfg.MongoDB); err != nil {
		log.Error("mongodb bootstrap error", zap.Error(err))
		os.Exit(1)
	}
	log.Info("mongodb bootstrap complete",
		zap.String("database", cfg.MongoDB.Database),
		zap.String("collection", cfg.MongoDB.CollectionLeads),
	)
	health.SetReadinessCheck(func(ctx context.Context) error {
		return mongoClient.Ping(ctx, nil)
	})

	// Leads module wiring
	leadsRepo := leads.NewMongoRepository(mongoClient, cfg.MongoDB)
	leadNotifier := leads.Notifier(leads.NoopNotifier{})
	if cfg.Leads.NotificationWebhookURL != "" {
		leadNotifier = leads.NewWebhookNotifier(cfg.Leads.NotificationWebhookURL)
	}
	leadsService := leads.NewService(leadsRepo, leadNotifier, cfg.Leads, log)
	leadsHandler := leads.NewHandler(leadsService, cfg.Leads, log)
	protectedLeadsHandler := leads.NewProtectionMiddleware(cfg.Leads, log)(http.HandlerFunc(leadsHandler.ServeHTTP))

	srv := httpserver.New(cfg.HTTP, log, func(r chi.Router) {
		r.Method(http.MethodPost, "/api/leads", protectedLeadsHandler)
	})

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info("server starting", zap.String("addr", cfg.HTTP.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", zap.Error(err))
			os.Exit(1)
		}
	}()

	<-quit
	log.Info("shutdown initiated")

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("graceful shutdown failed", zap.Error(err))
		os.Exit(1)
	}

	log.Info("server stopped")
}
