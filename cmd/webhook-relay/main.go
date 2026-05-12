// Package main is the Cloud Run entrypoint for the InfinitePay webhook relay service.
//
// This binary is intentionally minimal: it validates edge concerns (method, secret path,
// content-type, body size, throttle) and forwards the raw request bytes unchanged to the
// authenticated internal reconcile route on the core payments service.
//
// This service has NO MongoDB, NO Redis, NO InfinitePay credentials, and NO payment logic.
// It must NOT make any financial decisions.
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

	"github.com/villenneve/vil-core/internal/webhookrelay"
)

func main() {
	log, err := zap.NewProduction()
	if err != nil {
		os.Stderr.WriteString("logger init error: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer log.Sync() //nolint:errcheck

	cfg, err := webhookrelay.LoadConfig()
	if err != nil {
		log.Fatal("relay config load error", zap.Error(err))
	}

	throttle := webhookrelay.NewThrottle(cfg.SourceMaxInFlight, cfg.SourceRPM, cfg.GlobalMaxInFlight)

	// Evict idle per-source throttle state to prevent unbounded memory growth under abuse.
	sweepCtx, sweepCancel := context.WithCancel(context.Background())
	defer sweepCancel()
	throttle.StartSweep(sweepCtx, 5*time.Minute, 10*time.Minute)

	// Select primary identity token provider.
	// In production (Cloud Run) CoreAudience is set and the GCP metadata server is available.
	// In local dev CoreAudience is empty; the relay sends no Authorization header.
	var tokenProvider webhookrelay.TokenProvider
	if cfg.CoreAudience != "" {
		tokenProvider = webhookrelay.NewMetadataIDTokenProvider(cfg.CoreAudience)
	} else {
		tokenProvider = webhookrelay.NewStaticTokenProvider("") // no auth — local dev only
	}

	fwdClient := webhookrelay.NewForwardClient(cfg.CoreURL, cfg.CoreInternalToken, cfg.ForwardTimeoutSec, tokenProvider)
	relayHandler := webhookrelay.NewHandler(cfg, throttle, fwdClient, log)

	r := chi.NewRouter()

	// Health endpoints — stateless relay has no deep readiness dependency.
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

	// Provider-facing relay route.
	// The handler validates the exact secret path internally using constant-time comparison.
	// The wildcard suffix prevents the router from enumerating accepted path segments.
	// This is the ONLY provider-facing route on this service.
	r.Post("/v1/webhooks/infinitepay/*", relayHandler.ServeHTTP)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("relay started", zap.String("addr", cfg.HTTPAddr))
		serverErrors <- srv.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("relay server error", zap.Error(err))
		}
	case sig := <-shutdown:
		log.Info("relay shutdown signal received", zap.String("signal", sig.String()))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("relay graceful shutdown error", zap.Error(err))
		}
	}

	log.Info("relay stopped")
}
