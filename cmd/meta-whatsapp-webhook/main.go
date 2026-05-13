// Package main is the Cloud Run entrypoint for the Meta WhatsApp public webhook service.
//
// This binary is intentionally stateless: no MongoDB, no Redis, no business logic.
// It validates Meta's GET challenge and POST HMAC signature, then forwards raw
// notification bytes to the authenticated internal intake service (meta-whatsapp-api).
//
// Deploy requirements:
//   - --no-invoker-iam-check (public, unauthenticated access from Meta)
//   - VIL_WHATSAPP_META_VERIFY_TOKEN, VIL_WHATSAPP_META_APP_SECRET (loaded from Secret Manager)
//   - VIL_WHATSAPP_META_INTERNAL_INTAKE_URL, VIL_WHATSAPP_META_INTERNAL_AUDIENCE (derived from intake service)
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
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/platform/logger"
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

	if cfg.WhatsAppMeta.VerifyToken == "" {
		log.Fatal("whatsapp_meta.verify_token is required")
	}
	if cfg.WhatsAppMeta.AppSecret == "" {
		log.Fatal("whatsapp_meta.app_secret is required")
	}
	if cfg.WhatsAppMeta.InternalIntakeURL == "" {
		log.Fatal("whatsapp_meta.internal_intake_url is required")
	}

	// Select identity token provider.
	// In production (Cloud Run) InternalAudience is set and the metadata server is available.
	// In local dev InternalAudience is empty; no Authorization header is sent.
	var tp whatsapp.TokenProvider
	if cfg.WhatsAppMeta.InternalAudience != "" {
		tp = whatsapp.NewMetadataIDTokenProvider(cfg.WhatsAppMeta.InternalAudience)
	} else {
		tp = whatsapp.NewStaticTokenProvider("")
	}

	fwdClient := whatsapp.NewForwardClient(
		cfg.WhatsAppMeta.InternalIntakeURL,
		cfg.WhatsAppMeta.InternalVerifyAuthToken,
		10*time.Second,
		tp,
	)

	handler := whatsapp.NewPublicHandler(
		cfg.WhatsAppMeta.VerifyToken,
		cfg.WhatsAppMeta.AppSecret,
		cfg.WhatsAppMeta.BodyMaxBytes,
		fwdClient,
		log,
	)

	r := chi.NewRouter()

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

	// Meta webhook verification (GET) and notification intake (POST).
	r.Get("/v1/webhooks/meta/whatsapp", handler.ServeVerify)
	r.Post("/v1/webhooks/meta/whatsapp", handler.ServeNotification)

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      r,
		ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.HTTP.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.HTTP.IdleTimeout) * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("meta-whatsapp-webhook started", zap.String("addr", cfg.HTTP.Addr))
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

	log.Info("meta-whatsapp-webhook stopped")
}
