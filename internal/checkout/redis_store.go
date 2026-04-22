package checkout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// CheckoutStore defines Redis operations for the checkout domain.
// Redis is a fast path and coordination layer only — MongoDB is the source of truth.
type CheckoutStore interface {
	// Webhook idempotency — fast path before Mongo authoritative check.
	// CheckWebhookDedup checks both keys in a single pipeline round-trip.
	CheckWebhookDedup(ctx context.Context, transactionNSU, eventHash string) (txSeen, hashSeen bool, err error)
	SetWebhookTxSeen(ctx context.Context, transactionNSU string) error
	SetWebhookHashSeen(ctx context.Context, eventHash string) error

	// Distributed order lock. token must be a unique caller-generated value (e.g. UUID).
	// AcquireOrderLock returns ErrLockNotAcquired when the key is already held.
	AcquireOrderLock(ctx context.Context, orderNSU, token string) error
	// ReleaseOrderLock releases the lock only if the token matches (safe release).
	ReleaseOrderLock(ctx context.Context, orderNSU, token string) error

	// Order status cache. Invalidated on every order mutation.
	SetOrderStatus(ctx context.Context, orderNSU, status string) error
	GetOrderStatus(ctx context.Context, orderNSU string) (status string, found bool, err error)
	InvalidateOrderStatus(ctx context.Context, orderNSU string) error

	// Rate limiting counters. Callers pass a pre-hashed email to avoid PII in Redis keys.
	IncrCheckoutByIP(ctx context.Context, ip string) (int64, error)
	IncrCheckoutByEmailHash(ctx context.Context, emailHash string) (int64, error)
}

// TTL constants match the spec exactly.
const (
	ttlWebhookIdem    = 24 * time.Hour
	ttlOrderLock      = 15 * time.Second
	ttlCheckoutStatus = 30 * time.Minute
	ttlRateIP         = 1 * time.Minute
	ttlRateEmail      = 5 * time.Minute
)

// releaseLockScript atomically releases a lock only when the stored token matches.
// Returns 1 on success, 0 when the key is absent or token mismatch (already expired or stolen).
var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`)

// Key construction functions.
func keyWebhookTx(transactionNSU string) string { return "idem:webhook:tx:" + transactionNSU }
func keyWebhookHash(eventHash string) string    { return "idem:webhook:hash:" + eventHash }
func keyOrderLock(orderNSU string) string       { return "lock:order:" + orderNSU }
func keyCheckoutStatus(orderNSU string) string  { return "checkout:status:" + orderNSU }
func keyRateIP(ip string) string                { return "rate:checkout:ip:" + ip }
func keyRateEmail(emailHash string) string      { return "rate:checkout:email:" + emailHash }

type redisStore struct {
	client *redis.Client
}

// NewRedisStore returns a CheckoutStore backed by the provided Redis client.
func NewRedisStore(client *redis.Client) CheckoutStore {
	return &redisStore{client: client}
}

// CheckWebhookDedup checks both the transaction NSU and event hash keys in one
// pipeline round-trip. This is the Redis fast path; always follow a positive
// result with a Mongo authoritative check (ExistsByEventHash) before discarding.
func (r *redisStore) CheckWebhookDedup(ctx context.Context, transactionNSU, eventHash string) (bool, bool, error) {
	pipe := r.client.Pipeline()
	txCmd := pipe.Exists(ctx, keyWebhookTx(transactionNSU))
	hashCmd := pipe.Exists(ctx, keyWebhookHash(eventHash))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return false, false, fmt.Errorf("checkout dedup pipeline: %w", err)
	}
	return txCmd.Val() > 0, hashCmd.Val() > 0, nil
}

func (r *redisStore) SetWebhookTxSeen(ctx context.Context, transactionNSU string) error {
	if err := r.client.Set(ctx, keyWebhookTx(transactionNSU), "1", ttlWebhookIdem).Err(); err != nil {
		return fmt.Errorf("set webhook tx seen: %w", err)
	}
	return nil
}

func (r *redisStore) SetWebhookHashSeen(ctx context.Context, eventHash string) error {
	if err := r.client.Set(ctx, keyWebhookHash(eventHash), "1", ttlWebhookIdem).Err(); err != nil {
		return fmt.Errorf("set webhook hash seen: %w", err)
	}
	return nil
}

// AcquireOrderLock acquires a distributed lock using SET NX PX.
// Returns ErrLockNotAcquired if the lock is already held.
// token must be a unique per-caller value (e.g. a UUID) for safe release.
func (r *redisStore) AcquireOrderLock(ctx context.Context, orderNSU, token string) error {
	ok, err := r.client.SetNX(ctx, keyOrderLock(orderNSU), token, ttlOrderLock).Result()
	if err != nil {
		return fmt.Errorf("acquire order lock: %w", err)
	}
	if !ok {
		return ErrLockNotAcquired
	}
	return nil
}

// ReleaseOrderLock releases the lock only when the stored token matches.
// A token mismatch means the lock already expired or was acquired by another caller;
// this is not an error from the caller's perspective — it is logged and ignored.
func (r *redisStore) ReleaseOrderLock(ctx context.Context, orderNSU, token string) error {
	result, err := releaseLockScript.Run(ctx, r.client, []string{keyOrderLock(orderNSU)}, token).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("release order lock: %w", err)
	}
	_ = result // 0 = token mismatch or already expired; not treated as error
	return nil
}

func (r *redisStore) SetOrderStatus(ctx context.Context, orderNSU, status string) error {
	if err := r.client.Set(ctx, keyCheckoutStatus(orderNSU), status, ttlCheckoutStatus).Err(); err != nil {
		return fmt.Errorf("set order status cache: %w", err)
	}
	return nil
}

func (r *redisStore) GetOrderStatus(ctx context.Context, orderNSU string) (string, bool, error) {
	val, err := r.client.Get(ctx, keyCheckoutStatus(orderNSU)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get order status cache: %w", err)
	}
	return val, true, nil
}

func (r *redisStore) InvalidateOrderStatus(ctx context.Context, orderNSU string) error {
	if err := r.client.Del(ctx, keyCheckoutStatus(orderNSU)).Err(); err != nil {
		return fmt.Errorf("invalidate order status cache: %w", err)
	}
	return nil
}

// IncrCheckoutByIP increments the rate-limit counter for an IP.
// TTL is set on the first increment only. The counter is a sliding window
// approximation; exact enforcement is the service layer's responsibility.
func (r *redisStore) IncrCheckoutByIP(ctx context.Context, ip string) (int64, error) {
	key := keyRateIP(ip)
	cnt, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("incr rate by ip: %w", err)
	}
	if cnt == 1 {
		r.client.Expire(ctx, key, ttlRateIP)
	}
	return cnt, nil
}

// IncrCheckoutByEmailHash increments the rate-limit counter for a hashed email.
// Pass a hex-encoded hash (e.g. SHA-256) of the customer email — never the raw value.
func (r *redisStore) IncrCheckoutByEmailHash(ctx context.Context, emailHash string) (int64, error) {
	key := keyRateEmail(emailHash)
	cnt, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("incr rate by email hash: %w", err)
	}
	if cnt == 1 {
		r.client.Expire(ctx, key, ttlRateEmail)
	}
	return cnt, nil
}
