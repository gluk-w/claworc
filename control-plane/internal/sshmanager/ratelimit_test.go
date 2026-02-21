package sshmanager

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- RateLimiter unit tests ---

func TestNewRateLimiterDefaults(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimitConfig())
	if rl == nil {
		t.Fatal("NewRateLimiter returned nil")
	}
	if rl.config.MaxAttemptsPerMinute != DefaultMaxAttemptsPerMinute {
		t.Errorf("expected MaxAttemptsPerMinute %d, got %d", DefaultMaxAttemptsPerMinute, rl.config.MaxAttemptsPerMinute)
	}
	if rl.config.MaxConsecFailures != DefaultMaxConsecFailures {
		t.Errorf("expected MaxConsecFailures %d, got %d", DefaultMaxConsecFailures, rl.config.MaxConsecFailures)
	}
	if rl.config.BlockDuration != DefaultBlockDuration {
		t.Errorf("expected BlockDuration %v, got %v", DefaultBlockDuration, rl.config.BlockDuration)
	}
}

func TestAllowUnderLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 5,
		MaxConsecFailures:    3,
		BlockDuration:        1 * time.Minute,
	})

	// Should allow up to 5 attempts
	for i := 0; i < 5; i++ {
		if err := rl.Allow("test-inst"); err != nil {
			t.Errorf("attempt %d: unexpected error: %v", i+1, err)
		}
	}
}

func TestAllowExceedsPerMinuteLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 3,
		MaxConsecFailures:    10,
		BlockDuration:        1 * time.Minute,
	})

	for i := 0; i < 3; i++ {
		if err := rl.Allow("test-inst"); err != nil {
			t.Fatalf("attempt %d should be allowed: %v", i+1, err)
		}
	}

	// 4th attempt should be denied
	err := rl.Allow("test-inst")
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("expected 'rate limit exceeded' in error, got: %v", err)
	}
}

func TestAllowResetsAfterWindowExpires(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 2,
		MaxConsecFailures:    10,
		BlockDuration:        1 * time.Minute,
	})
	rl.nowFn = func() time.Time { return now }

	// Use up the limit
	rl.Allow("test-inst")
	rl.Allow("test-inst")

	// Should be blocked
	if err := rl.Allow("test-inst"); err == nil {
		t.Fatal("should be rate limited")
	}

	// Advance time past the 1 minute window
	now = now.Add(61 * time.Second)

	// Should be allowed again
	if err := rl.Allow("test-inst"); err != nil {
		t.Fatalf("should be allowed after window expiry: %v", err)
	}
}

func TestBlockAfterConsecutiveFailures(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100, // high limit so per-minute doesn't trigger
		MaxConsecFailures:    3,
		BlockDuration:        2 * time.Minute,
	})
	rl.nowFn = func() time.Time { return now }

	// Record 3 consecutive failures
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	// Should be blocked
	err := rl.Allow("test-inst")
	if err == nil {
		t.Fatal("expected block error after consecutive failures")
	}
	if !strings.Contains(err.Error(), "connection blocked") {
		t.Errorf("expected 'connection blocked' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "consecutive failures") {
		t.Errorf("expected 'consecutive failures' in error, got: %v", err)
	}
}

func TestBlockExpiresAfterDuration(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    2,
		BlockDuration:        1 * time.Minute,
	})
	rl.nowFn = func() time.Time { return now }

	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	// Should be blocked
	if err := rl.Allow("test-inst"); err == nil {
		t.Fatal("expected block error")
	}

	// Advance time past block duration
	now = now.Add(61 * time.Second)

	// Should be allowed again
	if err := rl.Allow("test-inst"); err != nil {
		t.Fatalf("should be unblocked after duration: %v", err)
	}
}

func TestRecordSuccessResetsFailureCounter(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    3,
		BlockDuration:        1 * time.Minute,
	})

	// Record 2 failures (not yet blocked)
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	// Success should reset the counter
	rl.RecordSuccess("test-inst")

	// Record 2 more failures (should not trigger block since counter was reset)
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	// Should still be allowed (only 2 consecutive failures)
	if err := rl.Allow("test-inst"); err != nil {
		t.Fatalf("should be allowed after success reset: %v", err)
	}
}

func TestRecordSuccessClearsBlock(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    2,
		BlockDuration:        5 * time.Minute,
	})
	rl.nowFn = func() time.Time { return now }

	// Trigger block
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	if err := rl.Allow("test-inst"); err == nil {
		t.Fatal("should be blocked")
	}

	// Success clears the block
	rl.RecordSuccess("test-inst")

	if err := rl.Allow("test-inst"); err != nil {
		t.Fatalf("should be unblocked after success: %v", err)
	}
}

func TestIndependentInstanceRateLimiting(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 2,
		MaxConsecFailures:    10,
		BlockDuration:        1 * time.Minute,
	})

	// Exhaust limit for inst-a
	rl.Allow("inst-a")
	rl.Allow("inst-a")

	// inst-a should be limited
	if err := rl.Allow("inst-a"); err == nil {
		t.Error("inst-a should be rate limited")
	}

	// inst-b should be unaffected
	if err := rl.Allow("inst-b"); err != nil {
		t.Errorf("inst-b should be allowed: %v", err)
	}
}

func TestIndependentInstanceBlocking(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    2,
		BlockDuration:        1 * time.Minute,
	})

	// Block inst-a
	rl.RecordFailure("inst-a")
	rl.RecordFailure("inst-a")

	if err := rl.Allow("inst-a"); err == nil {
		t.Error("inst-a should be blocked")
	}

	// inst-b should be unaffected
	if err := rl.Allow("inst-b"); err != nil {
		t.Errorf("inst-b should be allowed: %v", err)
	}
}

func TestGetStatusDefault(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimitConfig())

	status := rl.GetStatus("nonexistent")
	if status.RecentAttempts != 0 {
		t.Errorf("expected 0 recent attempts, got %d", status.RecentAttempts)
	}
	if status.Blocked {
		t.Error("should not be blocked")
	}
	if status.BlockedUntil != nil {
		t.Error("blocked_until should be nil")
	}
}

func TestGetStatusWithAttempts(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 10,
		MaxConsecFailures:    5,
		BlockDuration:        1 * time.Minute,
	})

	rl.Allow("test-inst")
	rl.Allow("test-inst")
	rl.Allow("test-inst")
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	status := rl.GetStatus("test-inst")
	if status.RecentAttempts != 3 {
		t.Errorf("expected 3 recent attempts, got %d", status.RecentAttempts)
	}
	if status.MaxAttemptsPerMin != 10 {
		t.Errorf("expected MaxAttemptsPerMin 10, got %d", status.MaxAttemptsPerMin)
	}
	if status.ConsecFailures != 2 {
		t.Errorf("expected 2 consecutive failures, got %d", status.ConsecFailures)
	}
	if status.MaxConsecFailures != 5 {
		t.Errorf("expected MaxConsecFailures 5, got %d", status.MaxConsecFailures)
	}
	if status.Blocked {
		t.Error("should not be blocked yet")
	}
}

func TestGetStatusWhenBlocked(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    2,
		BlockDuration:        5 * time.Minute,
	})
	rl.nowFn = func() time.Time { return now }

	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	status := rl.GetStatus("test-inst")
	if !status.Blocked {
		t.Error("should be blocked")
	}
	if status.BlockedUntil == nil {
		t.Fatal("BlockedUntil should be set")
	}
	expectedBlockedUntil := now.Add(5 * time.Minute)
	if !status.BlockedUntil.Equal(expectedBlockedUntil) {
		t.Errorf("expected BlockedUntil %v, got %v", expectedBlockedUntil, *status.BlockedUntil)
	}
}

func TestResetClearsState(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 2,
		MaxConsecFailures:    2,
		BlockDuration:        1 * time.Minute,
	})

	// Exhaust limits and block
	rl.Allow("test-inst")
	rl.Allow("test-inst")
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	// Should be both rate limited and blocked
	if err := rl.Allow("test-inst"); err == nil {
		t.Fatal("should be blocked or rate limited")
	}

	// Reset clears everything
	rl.Reset("test-inst")

	// Should be allowed again
	if err := rl.Allow("test-inst"); err != nil {
		t.Fatalf("should be allowed after reset: %v", err)
	}

	status := rl.GetStatus("test-inst")
	if status.ConsecFailures != 0 {
		t.Errorf("expected 0 consec failures after reset, got %d", status.ConsecFailures)
	}
}

func TestRateLimiterConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    100,
		BlockDuration:        1 * time.Minute,
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		inst := fmt.Sprintf("inst-%d", i%5)

		go func() {
			defer wg.Done()
			rl.Allow(inst)
		}()
		go func() {
			defer wg.Done()
			rl.RecordFailure(inst)
		}()
		go func() {
			defer wg.Done()
			rl.GetStatus(inst)
		}()
	}
	wg.Wait()
}

func TestPartialWindowPruning(t *testing.T) {
	now := time.Now()
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 3,
		MaxConsecFailures:    10,
		BlockDuration:        1 * time.Minute,
	})
	rl.nowFn = func() time.Time { return now }

	// Record 2 attempts at t=0
	rl.Allow("test-inst")
	rl.Allow("test-inst")

	// Advance 30 seconds (still within window)
	now = now.Add(30 * time.Second)

	// Record 1 more attempt
	rl.Allow("test-inst")

	// Should be at limit now (3 in window)
	if err := rl.Allow("test-inst"); err == nil {
		t.Fatal("should be rate limited (3 attempts in window)")
	}

	// Advance to 61s — the first 2 attempts should expire
	now = now.Add(31 * time.Second)

	// Should be allowed again (only 1 attempt in window now)
	if err := rl.Allow("test-inst"); err != nil {
		t.Fatalf("should be allowed after partial window expiry: %v", err)
	}
}

func TestFailureBelowThresholdDoesNotBlock(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    5,
		BlockDuration:        1 * time.Minute,
	})

	// Record 4 failures (below threshold of 5)
	for i := 0; i < 4; i++ {
		rl.RecordFailure("test-inst")
	}

	// Should not be blocked
	if err := rl.Allow("test-inst"); err != nil {
		t.Fatalf("should not be blocked with only 4 failures: %v", err)
	}
}

func TestExactlyAtThresholdBlocks(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    3,
		BlockDuration:        1 * time.Minute,
	})

	// Exactly at threshold
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")
	rl.RecordFailure("test-inst")

	if err := rl.Allow("test-inst"); err == nil {
		t.Fatal("should be blocked at exactly threshold")
	}
}

// --- SSHManager integration tests for rate limiting ---

func TestSSHManagerHasRateLimiter(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	if m.rateLimiter == nil {
		t.Fatal("SSHManager should have a rate limiter")
	}
}

func TestSSHManagerGetRateLimitStatus(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	status := m.GetRateLimitStatus("test-inst")
	if status.RecentAttempts != 0 {
		t.Errorf("expected 0 recent attempts for new instance, got %d", status.RecentAttempts)
	}
	if status.MaxAttemptsPerMin != DefaultMaxAttemptsPerMinute {
		t.Errorf("expected default max %d, got %d", DefaultMaxAttemptsPerMinute, status.MaxAttemptsPerMin)
	}
}

func TestSSHManagerResetRateLimit(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	// Record some failures
	m.rateLimiter.RecordFailure("test-inst")
	m.rateLimiter.RecordFailure("test-inst")

	status := m.GetRateLimitStatus("test-inst")
	if status.ConsecFailures != 2 {
		t.Fatalf("expected 2 failures, got %d", status.ConsecFailures)
	}

	m.ResetRateLimit("test-inst")

	status = m.GetRateLimitStatus("test-inst")
	if status.ConsecFailures != 0 {
		t.Errorf("expected 0 failures after reset, got %d", status.ConsecFailures)
	}
}

func TestSSHManagerGetRateLimiter(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	rl := m.GetRateLimiter()
	if rl == nil {
		t.Fatal("GetRateLimiter returned nil")
	}
	if rl != m.rateLimiter {
		t.Error("GetRateLimiter should return the same instance")
	}
}

func TestSSHManagerConnectRateLimited(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	// Override the rate limiter to have a very low limit
	m.rateLimiter = NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 1,
		MaxConsecFailures:    10,
		BlockDuration:        1 * time.Minute,
	})

	// First call will be allowed by the rate limiter but will fail because
	// there's no real SSH server. The important thing is the rate limiter allows it.
	ctx := t.Context()
	_, _ = m.Connect(ctx, "test-inst", "127.0.0.1", 22, "/nonexistent/key")

	// Second attempt should be rate limited (already used the 1 allowed attempt)
	_, err := m.Connect(ctx, "test-inst", "127.0.0.1", 22, "/nonexistent/key")
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestSSHManagerConnectBlockedAfterFailures(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.rateLimiter = NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    2,
		BlockDuration:        5 * time.Minute,
	})

	// Record 2 failures to trigger block
	m.rateLimiter.RecordFailure("test-inst")
	m.rateLimiter.RecordFailure("test-inst")

	// Should be blocked
	ctx := t.Context()
	_, err := m.Connect(ctx, "test-inst", "127.0.0.1", 22, "/nonexistent/key")
	if err == nil {
		t.Fatal("expected connection blocked error")
	}
	if !strings.Contains(err.Error(), "connection blocked") {
		t.Errorf("expected 'connection blocked' in error, got: %v", err)
	}
}

func TestSSHManagerRateLimitEventEmitted(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.rateLimiter = NewRateLimiter(RateLimitConfig{
		MaxAttemptsPerMinute: 100,
		MaxConsecFailures:    1,
		BlockDuration:        5 * time.Minute,
	})

	// Block the instance
	m.rateLimiter.RecordFailure("test-inst")

	// Try to connect (will be blocked by rate limiter)
	ctx := t.Context()
	m.Connect(ctx, "test-inst", "127.0.0.1", 22, "/nonexistent/key")

	// Check that a rate_limited event was emitted
	events := m.GetEvents("test-inst")
	found := false
	for _, e := range events {
		if e.Type == EventRateLimited {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected EventRateLimited event to be emitted")
	}
}

func TestDefaultRateLimitConfig(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	if cfg.MaxAttemptsPerMinute != 10 {
		t.Errorf("expected MaxAttemptsPerMinute 10, got %d", cfg.MaxAttemptsPerMinute)
	}
	if cfg.MaxConsecFailures != 5 {
		t.Errorf("expected MaxConsecFailures 5, got %d", cfg.MaxConsecFailures)
	}
	if cfg.BlockDuration != 5*time.Minute {
		t.Errorf("expected BlockDuration 5m, got %v", cfg.BlockDuration)
	}
}

func TestResetNonexistentInstance(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimitConfig())
	// Should not panic
	rl.Reset("nonexistent")
}

func TestRecordSuccessNonexistentInstance(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimitConfig())
	// Should not panic — creates state implicitly
	rl.RecordSuccess("new-inst")
	status := rl.GetStatus("new-inst")
	if status.ConsecFailures != 0 {
		t.Errorf("expected 0 failures, got %d", status.ConsecFailures)
	}
}

func TestRecordFailureNonexistentInstance(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimitConfig())
	rl.RecordFailure("new-inst")
	status := rl.GetStatus("new-inst")
	if status.ConsecFailures != 1 {
		t.Errorf("expected 1 failure, got %d", status.ConsecFailures)
	}
}
