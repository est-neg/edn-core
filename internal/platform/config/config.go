package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config is the top-level application configuration.
type Config struct {
	HTTP     HTTPConfig     `mapstructure:"http"`
	Log      LogConfig      `mapstructure:"log"`
	MongoDB  MongoConfig    `mapstructure:"mongodb"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Leads    LeadsConfig    `mapstructure:"leads"`
	Payments PaymentsConfig `mapstructure:"payments"`
	PubSub   PubSubConfig   `mapstructure:"pubsub"`
}

// PaymentsConfig holds payments module settings.
type PaymentsConfig struct {
	WebhookURL                string            `mapstructure:"webhook_url"`
	RedirectURL               string            `mapstructure:"redirect_url"`
	WebhookSecretPath         string            `mapstructure:"webhook_secret_path"`
	InternalVerifyAuthToken   string            `mapstructure:"internal_verify_auth_token"`
	RequestBodyMaxBytes       int64             `mapstructure:"request_body_max_bytes"`
	OutboxDispatchIntervalSec int               `mapstructure:"outbox_dispatch_interval_sec"`
	InfinitePay               InfinitePayConfig `mapstructure:"infinitepay"`
}

// InfinitePayConfig holds InfinitePay provider settings.
type InfinitePayConfig struct {
	BaseURL  string `mapstructure:"base_url"`
	APIToken string `mapstructure:"api_token"`
	Handle   string `mapstructure:"handle"`
}

// PubSubConfig holds Google Cloud Pub/Sub settings.
type PubSubConfig struct {
	ProjectID                   string `mapstructure:"project_id"`
	PaymentApprovedTopic        string `mapstructure:"payment_approved_topic"`
	PaymentApprovedSubscription string `mapstructure:"payment_approved_subscription"`
}

// MongoConfig holds MongoDB connection settings.
type MongoConfig struct {
	URI                       string `mapstructure:"uri"`
	Database                  string `mapstructure:"database"`
	ConnectTimeoutSec         int    `mapstructure:"connect_timeout_sec"`
	CollectionLeads           string `mapstructure:"collection_leads"`
	CollectionOrders          string `mapstructure:"collection_orders"`
	CollectionPayments        string `mapstructure:"collection_payments"`
	CollectionSubscriptions   string `mapstructure:"collection_subscriptions"`
	CollectionWebhookEvents   string `mapstructure:"collection_webhook_events"`
	CollectionOutboxEvents    string `mapstructure:"collection_outbox_events"`
	CollectionOrganizations   string `mapstructure:"collection_organizations"`
	CollectionTenants         string `mapstructure:"collection_tenants"`
	CollectionCatalogItems    string `mapstructure:"collection_catalog_items"`
	CollectionPackages        string `mapstructure:"collection_packages"`
	CollectionIdempotencyKeys string `mapstructure:"collection_idempotency_keys"`
	// CollectionVersionedPlans holds new versioned commercial plans (internal/plans).
	// Kept separate from CollectionPlans to preserve the legacy checkout flow.
	CollectionVersionedPlans string `mapstructure:"collection_versioned_plans"`
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	Addr            string `mapstructure:"addr"`
	Password        string `mapstructure:"password"`
	DB              int    `mapstructure:"db"`
	DialTimeoutSec  int    `mapstructure:"dial_timeout_sec"`
	ReadTimeoutSec  int    `mapstructure:"read_timeout_sec"`
	WriteTimeoutSec int    `mapstructure:"write_timeout_sec"`
	PoolSize        int    `mapstructure:"pool_size"`
}

// LeadsConfig holds lead-submission settings.
type LeadsConfig struct {
	AuthHeader             string `mapstructure:"auth_header"`
	AuthToken              string `mapstructure:"auth_token"`
	TenantID               string `mapstructure:"tenant_id"`
	MaxBodyBytes           int64  `mapstructure:"max_body_bytes"`
	DedupWindowSec         int    `mapstructure:"dedup_window_sec"`
	RateLimitRequests      int    `mapstructure:"rate_limit_requests_per_minute"`
	RateLimitWindowSec     int    `mapstructure:"rate_limit_window_sec"`
	MaxInFlight            int    `mapstructure:"max_in_flight"`
	NotificationWebhookURL string `mapstructure:"notification_webhook_url"`
	NotificationTimeoutSec int    `mapstructure:"notification_timeout_sec"`
}

// HTTPConfig holds HTTP server settings.
type HTTPConfig struct {
	Addr         string `mapstructure:"addr"`
	ReadTimeout  int    `mapstructure:"read_timeout_sec"`
	WriteTimeout int    `mapstructure:"write_timeout_sec"`
	IdleTimeout  int    `mapstructure:"idle_timeout_sec"`
	// CORSAllowedOrigins is a comma-separated list of origins allowed via CORS.
	// Example: "https://developer.funcionario.online,https://funcionario.online"
	CORSAllowedOrigins string `mapstructure:"cors_allowed_origins"`
	// AdminAuthToken is the full expected Authorization header value for admin endpoints.
	// Must be set explicitly (e.g. VIL_HTTP_ADMIN_AUTH_TOKEN). Fail-closed if empty.
	AdminAuthToken string `mapstructure:"admin_auth_token"`
}

// LogConfig holds logger settings.
type LogConfig struct {
	Level string `mapstructure:"level"`
	JSON  bool   `mapstructure:"json"`
}

// Load reads configuration from file and environment variables.
// Environment variables override file values.
// Prefix: VIL (e.g. VIL_HTTP_ADDR).
func Load() (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("http.addr", ":8080")
	v.SetDefault("http.read_timeout_sec", 10)
	v.SetDefault("http.write_timeout_sec", 30)
	v.SetDefault("http.idle_timeout_sec", 120)
	v.SetDefault("http.cors_allowed_origins", "")
	v.SetDefault("http.admin_auth_token", "")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.json", true)
	// mongodb — uri and database have no fallback but must be registered so
	// AutomaticEnv populates them during Unmarshal (Viper only resolves env
	// vars for keys it already knows about).
	v.SetDefault("mongodb.uri", "")
	v.SetDefault("mongodb.database", "")
	v.SetDefault("mongodb.connect_timeout_sec", 10)
	v.SetDefault("mongodb.collection_leads", "leads")
	v.SetDefault("mongodb.collection_orders", "orders")
	v.SetDefault("mongodb.collection_payments", "payments")
	v.SetDefault("mongodb.collection_subscriptions", "subscriptions")
	v.SetDefault("mongodb.collection_webhook_events", "webhook_events")
	v.SetDefault("mongodb.collection_outbox_events", "outbox_events")
	v.SetDefault("mongodb.collection_organizations", "organizations")
	v.SetDefault("mongodb.collection_tenants", "tenants")
	v.SetDefault("mongodb.collection_catalog_items", "catalog_items")
	v.SetDefault("mongodb.collection_packages", "packages")
	v.SetDefault("mongodb.collection_idempotency_keys", "idempotency_keys")
	v.SetDefault("mongodb.collection_versioned_plans", "versioned_plans")
	// redis
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.dial_timeout_sec", 5)
	v.SetDefault("redis.read_timeout_sec", 3)
	v.SetDefault("redis.write_timeout_sec", 3)
	v.SetDefault("redis.pool_size", 10)
	// leads
	v.SetDefault("leads.auth_header", "Authorization")
	v.SetDefault("leads.auth_token", "")
	v.SetDefault("leads.tenant_id", "seed-ten-fun-onl-001")
	v.SetDefault("leads.max_body_bytes", 16384) // 16 KiB
	v.SetDefault("leads.dedup_window_sec", 300) // 5 minutes
	v.SetDefault("leads.rate_limit_requests_per_minute", 60)
	v.SetDefault("leads.rate_limit_window_sec", 60)
	v.SetDefault("leads.max_in_flight", 10)
	v.SetDefault("leads.notification_timeout_sec", 10)
	v.SetDefault("leads.notification_webhook_url", "")
	// payments
	v.SetDefault("payments.webhook_url", "")
	v.SetDefault("payments.redirect_url", "")
	v.SetDefault("payments.webhook_secret_path", "")
	v.SetDefault("payments.internal_verify_auth_token", "")
	v.SetDefault("payments.request_body_max_bytes", 65536)
	v.SetDefault("payments.outbox_dispatch_interval_sec", 30)
	v.SetDefault("payments.infinitepay.base_url", "")
	v.SetDefault("payments.infinitepay.api_token", "")
	v.SetDefault("payments.infinitepay.handle", "")
	// pubsub
	v.SetDefault("pubsub.project_id", "")
	v.SetDefault("pubsub.payment_approved_topic", "")
	v.SetDefault("pubsub.payment_approved_subscription", "")

	// File lookup (optional)
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./configs")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !strings.Contains(err.Error(), "Not Found") && !isConfigNotFound(err, notFound) {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		// Config file is optional; fall through to env + defaults.
	}

	// Environment overrides
	v.SetEnvPrefix("VIL")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return cfg, nil
}

func isConfigNotFound(err error, _ viper.ConfigFileNotFoundError) bool {
	return strings.Contains(err.Error(), "Not Found")
}
