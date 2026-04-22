package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	count   int
	resetAt time.Time
}

// Limiter is a simple fixed-window in-memory limiter for low-volume endpoints.
// It is per-instance and intended as containment inside Cloud Run, not as a distributed perimeter.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]entry
	limit   int
	window  time.Duration
	now     func() time.Time
	ops     int
}

// New constructs a Limiter with the given limit and window.
func New(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		entries: make(map[string]entry),
		limit:   limit,
		window:  window,
		now:     now,
	}
}

// Allow increments the counter for a key and returns whether the request should be accepted.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l.limit <= 0 || l.window <= 0 {
		return true, 0
	}

	now := l.now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.ops++
	if l.ops%100 == 0 {
		l.sweepExpired(now)
	}

	current, ok := l.entries[key]
	if !ok || !now.Before(current.resetAt) {
		l.entries[key] = entry{count: 1, resetAt: now.Add(l.window)}
		return true, 0
	}

	if current.count >= l.limit {
		return false, current.resetAt.Sub(now)
	}

	current.count++
	l.entries[key] = current
	return true, 0
}

func (l *Limiter) sweepExpired(now time.Time) {
	for key, current := range l.entries {
		if !now.Before(current.resetAt) {
			delete(l.entries, key)
		}
	}
}
