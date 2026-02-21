// ratelimit.go implements SSH connection rate limiting for the sshproxy package.
//
// It protects against rapid reconnection storms and brute-force connection
// attempts by enforcing two complementary limits per instance:
//
//  1. Sliding-window rate limit: max 10 connection attempts per minute.
//  2. Consecutive-failure block: after 5 consecutive failures, the instance
//     is blocked for an escalating cooldown (starting at 30s, doubling each
//     time, capped at 5 minutes). A successful connection resets both the
//     failure counter and the block cooldown.
//
// All state is kept in-memory (no persistence needed) and keyed by instance
// ID (uint) for consistency with the rest of the sshproxy package.

package sshproxy

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	// rateLimitWindow is the sliding window for counting connection attempts.
	rateLimitWindow = 1 * time.Minute

	// rateLimitMaxAttempts is the max attempts allowed within rateLimitWindow.
	rateLimitMaxAttempts = 10

	// rateLimitFailureThreshold is consecutive failures before blocking.
	rateLimitFailureThreshold = 5

	// rateLimitInitialBlock is the initial block duration after hitting the failure threshold.
	rateLimitInitialBlock = 30 * time.Second

	// rateLimitMaxBlock caps exponential block growth.
	rateLimitMaxBlock = 5 * time.Minute
)

// ErrRateLimited is returned when a connection attempt is rejected by the rate limiter.
type ErrRateLimited struct {
	InstanceID uint
	Reason     string
	RetryAfter time.Duration
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("rate limited for instance %d: %s (retry after %s)", e.InstanceID, e.Reason, e.RetryAfter)
}

// instanceRateState tracks rate limiting state for a single instance.
type instanceRateState struct {
	// Sliding window of attempt timestamps.
	attempts []time.Time

	// Consecutive failure tracking.
	consecutiveFailures int
	blockedUntil        time.Time
	blockDuration       time.Duration // current block duration (doubles each block)
}

// RateLimiter enforces connection rate limits per instance.
type RateLimiter struct {
	mu     sync.Mutex
	states map[uint]*instanceRateState

	// Clock function for testing. Returns current time.
	nowFunc func() time.Time
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		states:  make(map[uint]*instanceRateState),
		nowFunc: time.Now,
	}
}

// getOrCreate returns the rate state for an instance, creating one if needed.
// Caller must hold rl.mu.
func (rl *RateLimiter) getOrCreate(instanceID uint) *instanceRateState {
	state, ok := rl.states[instanceID]
	if !ok {
		state = &instanceRateState{}
		rl.states[instanceID] = state
	}
	return state
}

// Allow checks whether a connection attempt for the given instance should be
// allowed. Returns nil if allowed, or an *ErrRateLimited if blocked.
func (rl *RateLimiter) Allow(instanceID uint) error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc()
	state := rl.getOrCreate(instanceID)

	// Check consecutive-failure block first.
	if !state.blockedUntil.IsZero() && now.Before(state.blockedUntil) {
		retryAfter := state.blockedUntil.Sub(now)
		log.Printf("SSH rate limit: instance %d blocked for %s after %d consecutive failures",
			instanceID, retryAfter.Round(time.Second), state.consecutiveFailures)
		return &ErrRateLimited{
			InstanceID: instanceID,
			Reason:     fmt.Sprintf("blocked after %d consecutive failures", state.consecutiveFailures),
			RetryAfter: retryAfter,
		}
	}

	// Sliding window: prune attempts older than the window.
	cutoff := now.Add(-rateLimitWindow)
	recent := state.attempts[:0]
	for _, t := range state.attempts {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	state.attempts = recent

	// Check window rate.
	if len(state.attempts) >= rateLimitMaxAttempts {
		oldest := state.attempts[0]
		retryAfter := oldest.Add(rateLimitWindow).Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		log.Printf("SSH rate limit: instance %d exceeded %d attempts in %s window",
			instanceID, rateLimitMaxAttempts, rateLimitWindow)
		return &ErrRateLimited{
			InstanceID: instanceID,
			Reason:     fmt.Sprintf("exceeded %d attempts in %s", rateLimitMaxAttempts, rateLimitWindow),
			RetryAfter: retryAfter,
		}
	}

	// Record this attempt.
	state.attempts = append(state.attempts, now)
	return nil
}

// RecordSuccess resets the consecutive failure counter and block state for
// the given instance. Called after a successful SSH connection.
func (rl *RateLimiter) RecordSuccess(instanceID uint) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	state, ok := rl.states[instanceID]
	if !ok {
		return
	}

	state.consecutiveFailures = 0
	state.blockedUntil = time.Time{}
	state.blockDuration = 0
}

// RecordFailure increments the consecutive failure counter for the given
// instance. If the failure threshold is reached, the instance is blocked
// for an escalating cooldown period.
func (rl *RateLimiter) RecordFailure(instanceID uint) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc()
	state := rl.getOrCreate(instanceID)

	state.consecutiveFailures++

	if state.consecutiveFailures >= rateLimitFailureThreshold {
		// Escalate block duration.
		if state.blockDuration == 0 {
			state.blockDuration = rateLimitInitialBlock
		} else {
			state.blockDuration *= 2
			if state.blockDuration > rateLimitMaxBlock {
				state.blockDuration = rateLimitMaxBlock
			}
		}
		state.blockedUntil = now.Add(state.blockDuration)
		log.Printf("SSH rate limit: instance %d blocked for %s after %d consecutive failures",
			instanceID, state.blockDuration, state.consecutiveFailures)
	}
}

// Reset removes all rate limiting state for the given instance.
func (rl *RateLimiter) Reset(instanceID uint) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.states, instanceID)
}

// GetState returns the current rate limiting state for an instance.
// Returns zero values if no state exists. Used for monitoring/debugging.
func (rl *RateLimiter) GetState(instanceID uint) (consecutiveFailures int, blockedUntil time.Time, attemptsInWindow int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	state, ok := rl.states[instanceID]
	if !ok {
		return 0, time.Time{}, 0
	}

	// Count recent attempts.
	now := rl.nowFunc()
	cutoff := now.Add(-rateLimitWindow)
	count := 0
	for _, t := range state.attempts {
		if t.After(cutoff) {
			count++
		}
	}

	return state.consecutiveFailures, state.blockedUntil, count
}
