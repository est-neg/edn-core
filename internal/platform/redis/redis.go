package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/villenneve/vil-core/internal/platform/config"
)

// New creates a Redis client, verifies connectivity with PING, and returns the
// client. The caller owns the client lifecycle and must call Close when done.
func New(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	dialTimeout := time.Duration(cfg.DialTimeoutSec) * time.Second
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}
	readTimeout := time.Duration(cfg.ReadTimeoutSec) * time.Second
	if readTimeout == 0 {
		readTimeout = 3 * time.Second
	}
	writeTimeout := time.Duration(cfg.WriteTimeoutSec) * time.Second
	if writeTimeout == 0 {
		writeTimeout = 3 * time.Second
	}
	poolSize := cfg.PoolSize
	if poolSize == 0 {
		poolSize = 10
	}

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  dialTimeout,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		PoolSize:     poolSize,
	})

	pingCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}
