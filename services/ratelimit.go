package services

import (
	"sync"
	"time"
)

// RateLimiter is an in-memory fixed-window counter keyed by an arbitrary
// string (typically "group:client-ip"). It is safe for concurrent use and is
// suitable for single-instance deployments; a shared store is needed for
// horizontally scaled web tiers.
type RateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	buckets map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

// NewRateLimiter creates a limiter allowing `limit` events per `window`.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit < 1 {
		limit = 1
	}
	return &RateLimiter{
		window:  window,
		limit:   limit,
		buckets: make(map[string]*bucket),
	}
}

// Allow records an event for key and reports whether it is within the limit.
func (rl *RateLimiter) Allow(key string) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok || now.After(b.reset) {
		rl.buckets[key] = &bucket{count: 1, reset: now.Add(rl.window)}
		return true
	}
	b.count++
	return b.count <= rl.limit
}

// Cleanup removes expired buckets to bound memory growth.
func (rl *RateLimiter) Cleanup() {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for k, b := range rl.buckets {
		if now.After(b.reset) {
			delete(rl.buckets, k)
		}
	}
}
