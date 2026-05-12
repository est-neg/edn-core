// Package webhookrelay implements the stateless public relay for InfinitePay webhook delivery.
// It validates edge concerns, preserves the raw request body byte-for-byte, and forwards
// to the authenticated internal core reconcile route.
//
// The relay has NO MongoDB, NO Redis, NO InfinitePay API token, and NO financial logic.
package webhookrelay

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds relay-specific runtime configuration.
// All values are loaded from environment variables (RELAY_* prefix).
// The relay intentionally does not share config with the core service.
type Config struct {
	// HTTPAddr is the TCP address for the relay HTTP server (default ":8080").
	HTTPAddr string
	// SecretPath is the opaque path segment that the provider must include in the webhook URL.
	// Must have at least 128 bits of entropy. Required.
	SecretPath string
	// NextSecretPath is an optional second valid secret path for planned rotation overlap.
	// Maximum overlap window is 15 minutes before the old path must be revoked.
	NextSecretPath string
	// CoreURL is the full URL of the internal reconcile route on the core service.
	// Example: https://edn-core-dev-xyz.run.app/internal/payments/providers/infinitepay/webhook-reconcile
	// Required.
	CoreURL string
	// CoreAudience is the base URL of the core Cloud Run service used as the OIDC audience
	// when fetching a Google-signed ID token from the GCP metadata server.
	// Required in production (Cloud Run). Leave empty for local development — the metadata
	// server is unavailable locally and the relay will send no Authorization header.
	// Example: https://edn-core-dev-xyz.run.app
	CoreAudience string
	// CoreInternalToken is the optional defense-in-depth token sent as X-EDN-Internal-Token.
	// IAM service-to-service auth via CoreAudience is the primary control; this is secondary only.
	CoreInternalToken string
	// BodyMaxBytes is the maximum accepted request body size in bytes (default 65536).
	BodyMaxBytes int64
	// ForwardTimeoutSec is the HTTP timeout for the relay→core hop in seconds (default 3).
	ForwardTimeoutSec int
	// SourceMaxInFlight is the per-source in-flight limit (default 8, v1 control).
	SourceMaxInFlight int
	// SourceRPM is the per-source rate limit in requests per minute (default 120, v1 control).
	SourceRPM int
	// GlobalMaxInFlight is the maximum total in-flight forwards per instance (default 32, v1 control).
	GlobalMaxInFlight int
}

// LoadConfig reads relay configuration from environment variables.
// Returns an error if any required variable is missing.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		HTTPAddr:          envStr("RELAY_HTTP_ADDR", ":8080"),
		SecretPath:        os.Getenv("RELAY_SECRET_PATH"),
		NextSecretPath:    os.Getenv("RELAY_NEXT_SECRET_PATH"),
		CoreURL:           os.Getenv("RELAY_CORE_URL"),
		CoreAudience:      os.Getenv("RELAY_CORE_AUDIENCE"),
		CoreInternalToken: os.Getenv("RELAY_CORE_INTERNAL_TOKEN"),
		BodyMaxBytes:      envInt64("RELAY_BODY_MAX_BYTES", 65536),
		ForwardTimeoutSec: envInt("RELAY_FORWARD_TIMEOUT_SEC", 3),
		SourceMaxInFlight: envInt("RELAY_SOURCE_MAX_IN_FLIGHT", 8),
		SourceRPM:         envInt("RELAY_SOURCE_RPM", 120),
		GlobalMaxInFlight: envInt("RELAY_GLOBAL_MAX_IN_FLIGHT", 32),
	}

	if cfg.SecretPath == "" {
		return nil, fmt.Errorf("RELAY_SECRET_PATH is required")
	}
	if cfg.CoreURL == "" {
		return nil, fmt.Errorf("RELAY_CORE_URL is required")
	}
	return cfg, nil
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
