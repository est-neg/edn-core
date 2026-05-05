package payments

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

	"github.com/villenneve/vil-core/internal/platform/ratelimit"
)

// NewCreateSessionRateLimiter returns a middleware that applies in-process rate limiting
// to POST /v1/checkout/sessions. Keyed by client IP.
// Limit: 10 requests per minute. Damage containment — not a distributed perimeter.
func NewCreateSessionRateLimiter(log *zap.Logger) func(http.Handler) http.Handler {
	return buildCreateSessionMiddleware(10, log)
}

// NewTrackRateLimiter returns a middleware that applies in-process rate limiting
// to POST /v1/checkout/sessions/track. Keyed by client IP.
// Limit: 30 requests per minute. Damage containment — not a distributed perimeter.
func NewTrackRateLimiter(log *zap.Logger) func(http.Handler) http.Handler {
	return buildTrackMiddleware(30, log)
}

// NewAdminSearchRateLimiter returns a middleware that applies in-process rate limiting
// to POST /api/v1/orders/search. Keyed by client IP.
// Limit: 20 requests per minute. Damage containment; auth is enforced by the handler.
func NewAdminSearchRateLimiter(log *zap.Logger) func(http.Handler) http.Handler {
	return buildAdminSearchMiddleware(20, log)
}

func buildCreateSessionMiddleware(limit int, log *zap.Logger) func(http.Handler) http.Handler {
	limiter := ratelimit.New(limit, time.Minute, nil)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := paymentsClientKey(r)
			allowed, retryAfter := limiter.Allow(key)
			if !allowed {
				log.Warn("checkout create session rate limited",
					zap.String("request_id", middleware.GetReqID(r.Context())),
					zap.String("path", r.URL.Path),
				)
				paymentsWriteRateLimited(w, retryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func buildTrackMiddleware(limit int, log *zap.Logger) func(http.Handler) http.Handler {
	limiter := ratelimit.New(limit, time.Minute, nil)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := paymentsClientKey(r)
			allowed, retryAfter := limiter.Allow(key)
			if !allowed {
				log.Warn("checkout track rate limited",
					zap.String("request_id", middleware.GetReqID(r.Context())),
					zap.String("path", r.URL.Path),
				)
				paymentsWriteRateLimited(w, retryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func buildAdminSearchMiddleware(limit int, log *zap.Logger) func(http.Handler) http.Handler {
	limiter := ratelimit.New(limit, time.Minute, nil)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := paymentsClientKey(r)
			allowed, retryAfter := limiter.Allow(key)
			if !allowed {
				log.Warn("admin search rate limited",
					zap.String("request_id", middleware.GetReqID(r.Context())),
					zap.String("path", r.URL.Path),
				)
				paymentsWriteRateLimited(w, retryAfter)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func paymentsClientKey(r *http.Request) string {
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

func paymentsWriteRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate_limited"})
}
