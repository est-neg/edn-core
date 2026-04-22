package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/villenneve/vil-core/internal/platform/health"
)

func TestLiveHandler_returns200(t *testing.T) {
	health.ClearReadinessCheck()
	t.Cleanup(health.ClearReadinessCheck)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)

	health.LiveHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp health.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status=ok, got %q", resp.Status)
	}
}

func TestReadyHandler_returns200(t *testing.T) {
	health.ClearReadinessCheck()
	t.Cleanup(health.ClearReadinessCheck)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	health.ReadyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp health.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Status != "ready" {
		t.Errorf("expected status=ready, got %q", resp.Status)
	}
}

func TestReadyHandler_returns503WhenDependencyFails(t *testing.T) {
	health.SetReadinessCheck(func(_ context.Context) error {
		return errors.New("mongodb unavailable")
	})
	t.Cleanup(health.ClearReadinessCheck)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	health.ReadyHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var resp health.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Status != "not_ready" {
		t.Errorf("expected status=not_ready, got %q", resp.Status)
	}
}
