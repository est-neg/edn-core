// Package main is the Cloud Run entrypoint for the Meta WhatsApp internal API service.
//
// This binary is authenticated and internal-only. It receives raw Meta webhook payloads
// forwarded by the public relay, parses them, deduplicates messages, and persists
// durable receipts in MongoDB.
//
// Deploy requirements:
//   - --no-allow-unauthenticated --ingress internal
//   - Cloud Run IAM service-to-service auth is the primary access control
//   - VIL_MONGODB_URI (loaded from Secret Manager)
//   - VIL_WHATSAPP_META_INTERNAL_VERIFY_AUTH_TOKEN (loaded from Secret Manager)
package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/platform/logger"
	"github.com/villenneve/vil-core/internal/platform/mongodb"
	"github.com/villenneve/vil-core/internal/whatsapp"
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
		log.Fatal("mongodb.uri is required")
	}

	ctx := context.Background()

	mongoClient, err := mongodb.New(ctx, cfg.MongoDB)
	if err != nil {
		log.Fatal("mongodb connect error", zap.Error(err))
	}
	defer mongoClient.Disconnect(context.Background()) //nolint:errcheck

	if err := mongodb.BootstrapWhatsAppStorage(ctx, mongoClient, cfg.MongoDB); err != nil {
		log.Fatal("whatsapp mongodb bootstrap error", zap.Error(err))
	}

	repo := whatsapp.NewMongoRepository(mongoClient, cfg.MongoDB)
	svc := whatsapp.NewIntakeService(repo, log)
	handler := whatsapp.NewInternalHandler(
		svc,
		cfg.WhatsAppMeta.InternalVerifyAuthToken,
		cfg.WhatsAppMeta.BodyMaxBytes,
		log,
	)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"status":"ok"}`) //nolint:errcheck
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"status":"ok"}`) //nolint:errcheck
	})

	// Internal intake route — NOT a public endpoint.
	// Must only be reachable from authenticated callers via Cloud Run IAM.
	// DEPLOY GATE: this service must run with --no-allow-unauthenticated --ingress internal.
	r.Post("/internal/whatsapp/meta/messages/intake", handler.ServeHTTP)

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      r,
		ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.HTTP.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.HTTP.IdleTimeout) * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("meta-whatsapp-api started", zap.String("addr", cfg.HTTP.Addr))
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown error", zap.Error(err))
		}
	}

	log.Info("meta-whatsapp-api stopped")
}
