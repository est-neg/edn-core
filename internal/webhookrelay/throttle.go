package webhookrelay

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// sourceState holds per-normalized-source throttle state.
// inFlight is accessed atomically. limiter is created once via LoadOrStore.
// lastUsedNano is updated atomically on every Acquire so the sweeper can evict stale entries.
type sourceState struct {
	inFlight     int32
	limiter      *rate.Limiter
	lastUsedNano atomic.Int64 // unix nanoseconds; set on each Acquire
}

// Throttle enforces source-aware and global in-flight limits.
//
// V1 controls (per spec):
//   - per source: max 8 in-flight slots
//   - per source: max 120 requests per minute
//   - global: max 32 in-flight per instance, no unbounded queue
//
// Per-source state is evicted by StartSweep after it has been idle for idleEvict
// (default 10 minutes) with zero in-flight requests. This bounds memory growth
// under abuse without racing active requests.
type Throttle struct {
	perSource      sync.Map // normalized source → *sourceState
	globalSem      chan struct{}
	maxPerSourceIF int32
	rpm            rate.Limit // per-second derived from RPM
	burst          int
}

// NewThrottle constructs a Throttle with the given per-source and global limits.
// maxSrcIF: max in-flight per source. rpm: max requests per minute per source.
// globalIF: max total in-flight across all sources.
func NewThrottle(maxSrcIF, rpm, globalIF int) *Throttle {
	if maxSrcIF <= 0 {
		maxSrcIF = 8
	}
	if rpm <= 0 {
		rpm = 120
	}
	if globalIF <= 0 {
		globalIF = 32
	}
	return &Throttle{
		globalSem:      make(chan struct{}, globalIF),
		maxPerSourceIF: int32(maxSrcIF),
		rpm:            rate.Limit(float64(rpm) / 60.0), // convert RPM to per-second
		burst:          rpm,                             // allow full-minute burst in one window
	}
}

// StartSweep starts a background goroutine that periodically evicts per-source
// state entries that have had zero in-flight requests for longer than idleEvict.
// The goroutine stops when ctx is cancelled. Typical values: interval=5m, idleEvict=10m.
func (t *Throttle) StartSweep(ctx context.Context, interval, idleEvict time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.sweep(idleEvict)
			}
		}
	}()
}

// sweep deletes per-source entries that are idle (inFlight==0) and have not been
// used within the idleEvict window.
func (t *Throttle) sweep(idleEvict time.Duration) {
	cutoff := time.Now().Add(-idleEvict).UnixNano()
	t.perSource.Range(func(k, v any) bool {
		ss := v.(*sourceState)
		if atomic.LoadInt32(&ss.inFlight) == 0 && ss.lastUsedNano.Load() < cutoff {
			t.perSource.Delete(k)
		}
		return true
	})
}

// Acquire attempts to acquire throttle capacity for the given normalized source.
// Returns true when capacity is available; the caller MUST call Release when done.
// Returns false immediately (backpressure) when any limit is exceeded.
func (t *Throttle) Acquire(_ context.Context, source string) bool {
	// 1. Global in-flight check (non-blocking).
	select {
	case t.globalSem <- struct{}{}:
	default:
		return false // global capacity exhausted
	}

	// 2. Per-source state (created once, evicted by sweep when idle).
	v, _ := t.perSource.LoadOrStore(source, &sourceState{
		limiter: rate.NewLimiter(t.rpm, t.burst),
	})
	ss := v.(*sourceState)
	ss.lastUsedNano.Store(time.Now().UnixNano())

	// 3. Per-source in-flight check.
	if atomic.AddInt32(&ss.inFlight, 1) > t.maxPerSourceIF {
		atomic.AddInt32(&ss.inFlight, -1)
		<-t.globalSem // release global slot
		return false
	}

	// 4. Per-source rate limit check (non-blocking token bucket).
	if !ss.limiter.Allow() {
		atomic.AddInt32(&ss.inFlight, -1)
		<-t.globalSem
		return false
	}

	return true
}

// Release decrements the in-flight counter for the given source and frees one global slot.
// Must be called exactly once after a successful Acquire.
func (t *Throttle) Release(source string) {
	if v, ok := t.perSource.Load(source); ok {
		ss := v.(*sourceState)
		atomic.AddInt32(&ss.inFlight, -1)
	}
	<-t.globalSem
}
