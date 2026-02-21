package sshmanager

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// Rate limiting defaults. Two independent mechanisms protect against connection storms:
//   - Sliding-window rate limit: max attempts per minute per instance.
//   - Consecutive failure block: after N failures in a row, the instance is
//     temporarily blocked for BlockDuration.
const (
	DefaultMaxAttemptsPerMinute = 10
	DefaultMaxConsecFailures    = 5
	DefaultBlockDuration        = 5 * time.Minute
)

// New event types for rate limiting.
const (
	EventRateLimited EventType = "rate_limited"
)

// RateLimitConfig holds configuration for the SSH connection rate limiter.
type RateLimitConfig struct {
	MaxAttemptsPerMinute int           // Maximum connection attempts per instance per minute
	MaxConsecFailures    int           // Consecutive failures before temporary block
	BlockDuration        time.Duration // Duration to block after max consecutive failures
}

// DefaultRateLimitConfig returns the default rate limit configuration.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		MaxAttemptsPerMinute: DefaultMaxAttemptsPerMinute,
		MaxConsecFailures:    DefaultMaxConsecFailures,
		BlockDuration:        DefaultBlockDuration,
	}
}

// instanceRateState tracks rate limiting state for a single instance.
type instanceRateState struct {
	attempts        []time.Time // timestamps of recent connection attempts
	consecFailures  int         // consecutive failure count
	blockedUntil    time.Time   // when the instance becomes unblocked
}

// RateLimiter enforces rate limits on SSH connection attempts per instance.
// It tracks connection attempts within a sliding window and blocks instances
// that exceed the configured thresholds.
type RateLimiter struct {
	mu     sync.Mutex
	config RateLimitConfig
	state  map[string]*instanceRateState
	nowFn  func() time.Time // injectable clock for testing
}

// NewRateLimiter creates a new RateLimiter with the given configuration.
func NewRateLimiter(config RateLimitConfig) *RateLimiter {
	return &RateLimiter{
		config: config,
		state:  make(map[string]*instanceRateState),
		nowFn:  time.Now,
	}
}

// Allow checks whether a connection attempt for the given instance is allowed.
// Returns nil if allowed, or an error describing why the attempt was denied.
func (rl *RateLimiter) Allow(instanceName string) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFn()
	s := rl.getOrCreateState(instanceName)

	// Check if the instance is temporarily blocked
	if now.Before(s.blockedUntil) {
		remaining := s.blockedUntil.Sub(now).Truncate(time.Second)
		log.Printf("[ssh] rate limit: instance %s is blocked for %s (consecutive failures: %d)",
			logutil.SanitizeForLog(instanceName), remaining, s.consecFailures)
		return fmt.Errorf("connection blocked for %s due to %d consecutive failures; retry after %s",
			logutil.SanitizeForLog(instanceName), s.consecFailures, remaining)
	}

	// Prune attempts older than 1 minute
	cutoff := now.Add(-1 * time.Minute)
	pruned := s.attempts[:0]
	for _, t := range s.attempts {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	s.attempts = pruned

	// Check per-minute rate limit
	if len(s.attempts) >= rl.config.MaxAttemptsPerMinute {
		log.Printf("[ssh] rate limit: instance %s exceeded %d attempts/min",
			logutil.SanitizeForLog(instanceName), rl.config.MaxAttemptsPerMinute)
		return fmt.Errorf("rate limit exceeded for %s: %d connection attempts in the last minute (max %d)",
			logutil.SanitizeForLog(instanceName), len(s.attempts), rl.config.MaxAttemptsPerMinute)
	}

	// Record this attempt
	s.attempts = append(s.attempts, now)
	return nil
}

// RecordSuccess resets the consecutive failure counter for the given instance.
func (rl *RateLimiter) RecordSuccess(instanceName string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	s := rl.getOrCreateState(instanceName)
	s.consecFailures = 0
	s.blockedUntil = time.Time{} // clear any active block
}

// RecordFailure increments the consecutive failure counter for the given instance.
// If the counter reaches the configured threshold, the instance is temporarily blocked.
func (rl *RateLimiter) RecordFailure(instanceName string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFn()
	s := rl.getOrCreateState(instanceName)
	s.consecFailures++

	if s.consecFailures >= rl.config.MaxConsecFailures {
		s.blockedUntil = now.Add(rl.config.BlockDuration)
		log.Printf("[ssh] rate limit: blocking instance %s until %s (%d consecutive failures)",
			logutil.SanitizeForLog(instanceName), s.blockedUntil.Format(time.RFC3339), s.consecFailures)
	}
}

// GetStatus returns the current rate limit status for the given instance.
func (rl *RateLimiter) GetStatus(instanceName string) RateLimitStatus {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFn()
	s, ok := rl.state[instanceName]
	if !ok {
		return RateLimitStatus{
			MaxAttemptsPerMin: rl.config.MaxAttemptsPerMinute,
			MaxConsecFailures: rl.config.MaxConsecFailures,
		}
	}

	// Count recent attempts in the last minute
	cutoff := now.Add(-1 * time.Minute)
	recentAttempts := 0
	for _, t := range s.attempts {
		if t.After(cutoff) {
			recentAttempts++
		}
	}

	blocked := now.Before(s.blockedUntil)
	var blockedUntil *time.Time
	if blocked {
		bu := s.blockedUntil
		blockedUntil = &bu
	}

	return RateLimitStatus{
		RecentAttempts:     recentAttempts,
		MaxAttemptsPerMin:  rl.config.MaxAttemptsPerMinute,
		ConsecFailures:     s.consecFailures,
		MaxConsecFailures:  rl.config.MaxConsecFailures,
		Blocked:            blocked,
		BlockedUntil:       blockedUntil,
	}
}

// Reset clears all rate limiting state for the given instance.
func (rl *RateLimiter) Reset(instanceName string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.state, instanceName)
}

// RateLimitStatus represents the current rate limit state for an instance.
type RateLimitStatus struct {
	RecentAttempts    int        `json:"recent_attempts"`
	MaxAttemptsPerMin int        `json:"max_attempts_per_min"`
	ConsecFailures    int        `json:"consec_failures"`
	MaxConsecFailures int        `json:"max_consec_failures"`
	Blocked           bool       `json:"blocked"`
	BlockedUntil      *time.Time `json:"blocked_until,omitempty"`
}

// getOrCreateState returns the rate state for an instance, creating it if needed.
// Must be called with rl.mu held.
func (rl *RateLimiter) getOrCreateState(instanceName string) *instanceRateState {
	s, ok := rl.state[instanceName]
	if !ok {
		s = &instanceRateState{}
		rl.state[instanceName] = s
	}
	return s
}
