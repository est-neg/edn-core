package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config is the top-level application configuration.
type Config struct {
	HTTP HTTPConfig `mapstructure:"http"`
	Log  LogConfig  `mapstructure:"log"`
}

// HTTPConfig holds HTTP server settings.
type HTTPConfig struct {
	Addr         string `mapstructure:"addr"`
	ReadTimeout  int    `mapstructure:"read_timeout_sec"`
	WriteTimeout int    `mapstructure:"write_timeout_sec"`
	IdleTimeout  int    `mapstructure:"idle_timeout_sec"`
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
	v.SetDefault("log.level", "info")
	v.SetDefault("log.json", true)

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
