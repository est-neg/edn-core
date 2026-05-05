package payments

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// TestCreateSessionRateLimiter_Returns429 verifies that once the in-process rate limit is
// exhausted for POST /v1/checkout/sessions the middleware returns 429 with a Retry-After header.
func TestCreateSessionRateLimiter_Returns429(t *testing.T) {
	var calls int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	})

	mw := buildCreateSessionMiddleware(1, zap.NewNop())
	handler := mw(inner)

	send := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/checkout/sessions", nil)
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	first := send("10.0.0.3:1111")
	if first.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", first.Code)
	}

	second := send("10.0.0.3:2222")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", second.Code)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("expected Retry-After header on 429 response")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("wrapped handler must run exactly once; ran %d times", calls)
	}
}

// TestTrackRateLimiter_Returns429 verifies that once the in-process rate limit is
// exhausted the middleware returns 429 with a Retry-After header and stops the
// wrapped handler from running.
func TestTrackRateLimiter_Returns429(t *testing.T) {
	var calls int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	})

	// Limit of 1 so the second request triggers the 429.
	mw := buildTrackMiddleware(1, zap.NewNop())
	handler := mw(inner)

	send := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/checkout/sessions/track", nil)
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	first := send("10.0.0.1:1111")
	if first.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", first.Code)
	}

	second := send("10.0.0.1:2222")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", second.Code)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("expected Retry-After header on 429 response")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("wrapped handler must run exactly once; ran %d times", calls)
	}
}

// TestAdminSearchRateLimiter_Returns429 verifies that the admin-search rate limiter
// returns 429 with Retry-After after the per-IP limit is exceeded.
func TestAdminSearchRateLimiter_Returns429(t *testing.T) {
	var calls int32
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	})

	mw := buildAdminSearchMiddleware(1, zap.NewNop())
	handler := mw(inner)

	send := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/search", nil)
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	first := send("10.0.0.2:3333")
	if first.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", first.Code)
	}

	second := send("10.0.0.2:4444")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: expected 429, got %d", second.Code)
	}
	if got := second.Header().Get("Retry-After"); got == "" {
		t.Error("expected Retry-After header on 429 response")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("wrapped handler must run exactly once; ran %d times", calls)
	}
}

// TestRateLimiter_DifferentIPs_NotRateLimited verifies that distinct client IPs
// each get their own window and do not trigger the limit for one another.
func TestRateLimiter_DifferentIPs_NotRateLimited(t *testing.T) {
	mw := buildTrackMiddleware(1, zap.NewNop())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, addr := range []string{"10.1.1.1:1", "10.1.1.2:1", "10.1.1.3:1"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/checkout/sessions/track", nil)
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("addr %s: expected 200, got %d", addr, rr.Code)
		}
	}
}
