package leads

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/platform/config"
	"github.com/villenneve/vil-core/internal/platform/ratelimit"
)

// NewProtectionMiddleware applies low-volume endpoint protections that fit inside Cloud Run itself.
// This limiter is per-instance and intended as damage containment, not as a distributed perimeter.
func NewProtectionMiddleware(cfg config.LeadsConfig, log *zap.Logger) func(http.Handler) http.Handler {
	window := time.Duration(cfg.RateLimitWindowSec) * time.Second
	if window <= 0 {
		window = time.Minute
	}

	requests := cfg.RateLimitRequests
	if requests <= 0 {
		requests = 60
	}

	maxInFlight := cfg.MaxInFlight
	if maxInFlight <= 0 {
		maxInFlight = 10
	}

	limiter := ratelimit.New(requests, window, nil)
	inFlight := make(chan struct{}, maxInFlight)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientKey(r)

			allowed, retryAfter := limiter.Allow(key)
			if !allowed {
				log.Warn("lead request rate limited",
					zap.String("request_id", middleware.GetReqID(r.Context())),
					zap.String("path", r.URL.Path),
					zap.String("client_key", key),
				)
				writeRateLimited(w, retryAfter)
				return
			}

			select {
			case inFlight <- struct{}{}:
				defer func() { <-inFlight }()
			default:
				log.Warn("lead request concurrency limited",
					zap.String("request_id", middleware.GetReqID(r.Context())),
					zap.String("path", r.URL.Path),
					zap.String("client_key", key),
				)
				writeRateLimited(w, time.Second)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", formatRetryAfter(retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate_limited"})
}

func formatRetryAfter(value time.Duration) string {
	seconds := int(math.Ceil(value.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func clientKey(r *http.Request) string {
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return "unknown"
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}

	return remoteAddr
}
