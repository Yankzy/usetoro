// Package resilience provides circuit breakers and rate limiting for fault tolerance
package resilience

import (
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter provides per-connection rate limiting
type RateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
}

// NewRateLimiter creates a new rate limiter
// rate: requests per second
// burst: burst size
func NewRateLimiter(rateLimit float64, burst int) *RateLimiter {
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     rate.Limit(rateLimit),
		burst:    burst,
	}
}

// Allow checks if a request for the given key is allowed
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.RLock()
	limiter, exists := rl.limiters[key]
	rl.mu.RUnlock()

	if !exists {
		limiter = rate.NewLimiter(rl.rate, rl.burst)
		rl.mu.Lock()
		rl.limiters[key] = limiter
		rl.mu.Unlock()
	}

	return limiter.Allow()
}

// Reset removes the limiter for a given key (useful for cleanup)
func (rl *RateLimiter) Reset(key string) {
	rl.mu.Lock()
	delete(rl.limiters, key)
	rl.mu.Unlock()
}
