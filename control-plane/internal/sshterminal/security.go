package sshterminal

import (
	"fmt"
	"sync"
	"time"
)

// AllowedShells is the whitelist of shells that may be started via
// CreateInteractiveSession. Any shell not in this list is rejected.
var AllowedShells = []string{
	"/bin/bash",
	"/bin/sh",
	"/bin/zsh",
}

// ValidateShell checks whether the given shell path is in the AllowedShells
// whitelist. An empty string is allowed and defaults to DefaultShell.
func ValidateShell(shell string) error {
	if shell == "" {
		return nil // will default to DefaultShell in CreateInteractiveSession
	}
	for _, allowed := range AllowedShells {
		if shell == allowed {
			return nil
		}
	}
	return fmt.Errorf("shell %q is not allowed; permitted shells: %v", shell, AllowedShells)
}

// Security-related constants for terminal sessions.
const (
	// MaxInputMessageSize is the maximum size in bytes for a single input
	// message sent over the WebSocket. Messages larger than this are rejected.
	MaxInputMessageSize = 64 * 1024 // 64 KB

	// MaxTermCols is the maximum allowed terminal width.
	MaxTermCols = 500
	// MaxTermRows is the maximum allowed terminal height.
	MaxTermRows = 200

	// MessageRateLimit is the maximum number of messages per second from a client.
	MessageRateLimit = 100
	// MessageRateBurst is the burst allowance for the rate limiter.
	MessageRateBurst = 200
)

// RateLimiter implements a simple token bucket rate limiter for WebSocket messages.
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter creates a rate limiter with the given rate (tokens/sec) and burst size.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	return &RateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: rate,
		lastRefill: time.Now(),
	}
}

// Allow returns true if a message is permitted, consuming one token.
// Returns false if the rate limit has been exceeded.
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.lastRefill = now

	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}

	if rl.tokens < 1 {
		return false
	}
	rl.tokens--
	return true
}
