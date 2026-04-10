package config_test

import (
	"os"
	"testing"

	"github.com/villenneve/vil-core/internal/platform/config"
)

func TestLoad_defaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	if cfg.HTTP.Addr == "" {
		t.Error("expected default HTTP addr to be set")
	}

	if cfg.Log.Level == "" {
		t.Error("expected default log level to be set")
	}
}

func TestLoad_envOverride(t *testing.T) {
	t.Setenv("VIL_HTTP_ADDR", ":9090")
	t.Setenv("VIL_LOG_LEVEL", "debug")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	if cfg.HTTP.Addr != ":9090" {
		t.Errorf("expected addr :9090, got %q", cfg.HTTP.Addr)
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("expected level debug, got %q", cfg.Log.Level)
	}

	os.Unsetenv("VIL_HTTP_ADDR")
	os.Unsetenv("VIL_LOG_LEVEL")
}
