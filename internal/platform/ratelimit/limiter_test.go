package ratelimit_test

import (
	"testing"
	"time"

	"github.com/villenneve/vil-core/internal/platform/ratelimit"
)

func TestLimiter_AllowsUntilWindowExhausted(t *testing.T) {
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	limiter := ratelimit.New(2, time.Minute, func() time.Time { return now })

	if ok, _ := limiter.Allow("client"); !ok {
		t.Fatal("expected first request to pass")
	}
	if ok, _ := limiter.Allow("client"); !ok {
		t.Fatal("expected second request to pass")
	}
	if ok, retryAfter := limiter.Allow("client"); ok {
		t.Fatal("expected third request to be blocked")
	} else if retryAfter <= 0 {
		t.Fatalf("expected positive retryAfter, got %v", retryAfter)
	}
}

func TestLimiter_ResetsAfterWindow(t *testing.T) {
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	limiter := ratelimit.New(1, time.Minute, func() time.Time { return now })

	if ok, _ := limiter.Allow("client"); !ok {
		t.Fatal("expected first request to pass")
	}
	if ok, _ := limiter.Allow("client"); ok {
		t.Fatal("expected request inside same window to be blocked")
	}

	now = now.Add(time.Minute)
	if ok, _ := limiter.Allow("client"); !ok {
		t.Fatal("expected request after window reset to pass")
	}
}
