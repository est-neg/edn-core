package httpserver

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/platform/health"
)

// New builds and returns the HTTP server with the application router mounted.
// Optional register functions may be provided to mount additional routes.
func New(cfg config.HTTPConfig, log *zap.Logger, register ...func(chi.Router)) *http.Server {
	r := chi.NewRouter()

	// Core middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger(log))
	r.Use(middleware.Recoverer)

	// Health endpoints — not gated by auth
	r.Get("/livez", health.LiveHandler)
	r.Get("/readyz", health.ReadyHandler)

	// Feature routes registered by callers
	for _, fn := range register {
		fn(r)
	}

	// Versioned API prefix — extend per feature module
	r.Route("/api/v1", func(r chi.Router) {
		// Feature routes will be registered here as modules are added:
		// r.Mount("/users", users.Router(deps))
	})

	return &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  time.Duration(cfg.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.WriteTimeout) * time.Second,
		IdleTimeout:  time.Duration(cfg.IdleTimeout) * time.Second,
	}
}

// requestLogger adapts chi's built-in logger to zap.
func requestLogger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			fields := []zapcore.Field{
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("request_id", middleware.GetReqID(r.Context())),
				zap.String("remote_addr", r.RemoteAddr),
				zap.Int("status", ww.Status()),
				zap.Int("bytes", ww.BytesWritten()),
			}

			log.Info("request", fields...)
		})
	}
}
