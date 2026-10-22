package auth

import (
	"sync"
	"time"
)

type limiterBucket struct {
	start time.Time
	count int
}

// Limiter is a fixed-window, in-memory rate limiter. It is intentionally
// per-process for P0; multi-instance deployments need a shared store.
type Limiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	now       func() time.Time
	buckets   map[string]*limiterBucket
	lastSweep time.Time
}

// NewLimiter allows at most max events per window per key.
func NewLimiter(max int, window time.Duration) *Limiter {
	return newLimiter(max, window, time.Now)
}

func newLimiter(max int, window time.Duration, now func() time.Time) *Limiter {
	return &Limiter{
		max:       max,
		window:    window,
		now:       now,
		buckets:   make(map[string]*limiterBucket),
		lastSweep: now(),
	}
}

// Allow consumes one event. When denied it also reports how long the caller
// should wait before retrying.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil || l.max <= 0 || l.window <= 0 {
		return true, 0
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.buckets[key] = &limiterBucket{start: now, count: 1}
		return true, 0
	}
	if b.count >= l.max {
		retry := l.window - now.Sub(b.start)
		if retry < 0 {
			retry = 0
		}
		return false, retry
	}
	b.count++
	return true, 0
}

func (l *Limiter) sweepLocked(now time.Time) {
	if len(l.buckets) < 1024 && now.Sub(l.lastSweep) < l.window {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.start) >= l.window {
			delete(l.buckets, k)
		}
	}
	l.lastSweep = now
}
