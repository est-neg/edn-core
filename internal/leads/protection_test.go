package leads_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/villenneve/vil-core/internal/leads"
	"github.com/villenneve/vil-core/internal/platform/config"
)

func TestProtectionMiddleware_RateLimitsRepeatedRequests(t *testing.T) {
	var calls int32
	middleware := leads.NewProtectionMiddleware(config.LeadsConfig{
		RateLimitRequests:  1,
		RateLimitWindowSec: 60,
		MaxInFlight:        1,
	}, zap.NewNop())

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))

	req1 := httptest.NewRequest(http.MethodPost, "/api/leads", nil)
	req1.RemoteAddr = "203.0.113.10:1234"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/leads", nil)
	req2.RemoteAddr = "203.0.113.10:9999"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429, got %d", rec2.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected wrapped handler to run once, got %d", got)
	}
}

func TestProtectionMiddleware_LimitsConcurrentRequests(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	middleware := leads.NewProtectionMiddleware(config.LeadsConfig{
		RateLimitRequests:  10,
		RateLimitWindowSec: 60,
		MaxInFlight:        1,
	}, zap.NewNop())

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/leads", nil)
		req.RemoteAddr = "203.0.113.20:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		firstDone <- rec
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not start in time")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/leads", nil)
	secondReq.RemoteAddr = "203.0.113.21:1234"
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)

	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second concurrent request 429, got %d", secondRec.Code)
	}

	close(release)
	select {
	case firstRec := <-firstDone:
		if firstRec.Code != http.StatusOK {
			t.Fatalf("expected first request 200, got %d", firstRec.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not complete in time")
	}
}
