// Package ratelimit implements per-key token bucket rate limiting with a
// bounded number of tracked keys.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows rate events per second per key, with bursts up to burst.
// A nil *Limiter allows everything.
type Limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	maxKeys int
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a limiter, or nil (no limiting) when rate is not positive.
func New(rate float64, burst int, maxKeys int) *Limiter {
	if rate <= 0 {
		return nil
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		rate:    rate,
		burst:   float64(burst),
		maxKeys: maxKeys,
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow consumes one token for key. When no token is available it returns
// false and how long until one will be.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.evict(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
		b.last = now
	}
	if b.tokens < 1 {
		wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
		return false, wait
	}
	b.tokens--
	return true, 0
}

// evict drops buckets that have refilled completely (their state carries no
// information), then arbitrary buckets if the map is still full.
func (l *Limiter) evict(now time.Time) {
	refill := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) >= refill {
			delete(l.buckets, k)
		}
	}
	for k := range l.buckets {
		if len(l.buckets) < l.maxKeys {
			break
		}
		delete(l.buckets, k)
	}
}

// Len returns the number of tracked keys.
func (l *Limiter) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
