// Package resilience provides circuit breakers and rate limiting for fault tolerance
package resilience

import (
	"sync"
)

// FailedAttemptsTracker tracks failed authentication attempts per connection
type FailedAttemptsTracker struct {
	attempts    map[string]int
	mu          sync.RWMutex
	maxAttempts int
}

// NewFailedAttemptsTracker creates a new tracker
func NewFailedAttemptsTracker(maxAttempts int) *FailedAttemptsTracker {
	return &FailedAttemptsTracker{
		attempts:    make(map[string]int),
		maxAttempts: maxAttempts,
	}
}

// Increment increments the failed attempt count for a key
// Returns the new count
func (f *FailedAttemptsTracker) Increment(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts[key]++
	return f.attempts[key]
}

// IsBlocked returns true if the key has exceeded max attempts
func (f *FailedAttemptsTracker) IsBlocked(key string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.attempts[key] >= f.maxAttempts
}

// Reset resets the counter for a key
func (f *FailedAttemptsTracker) Reset(key string) {
	f.mu.Lock()
	delete(f.attempts, key)
	f.mu.Unlock()
}
