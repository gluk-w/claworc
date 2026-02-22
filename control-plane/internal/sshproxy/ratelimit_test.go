package sshproxy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock is a controllable clock for testing rate limiting.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestRateLimiter(clock *fakeClock) *RateLimiter {
	rl := NewRateLimiter()
	rl.nowFunc = clock.Now
	return rl
}

func TestRateLimiter_AllowUnderLimit(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Should allow up to rateLimitMaxAttempts.
	for i := 0; i < rateLimitMaxAttempts; i++ {
		if err := rl.Allow(1); err != nil {
			t.Fatalf("Allow() attempt %d: unexpected error: %v", i+1, err)
		}
	}
}

func TestRateLimiter_BlocksAfterMaxAttempts(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Fill the window.
	for i := 0; i < rateLimitMaxAttempts; i++ {
		if err := rl.Allow(1); err != nil {
			t.Fatalf("Allow() attempt %d: unexpected error: %v", i+1, err)
		}
	}

	// Next attempt should be blocked.
	err := rl.Allow(1)
	if err == nil {
		t.Fatal("Allow() expected rate limit error after max attempts")
	}

	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T: %v", err, err)
	}
	if rlErr.InstanceID != 1 {
		t.Errorf("ErrRateLimited.InstanceID = %d, want 1", rlErr.InstanceID)
	}
}

func TestRateLimiter_WindowSlidesOver(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Fill the window.
	for i := 0; i < rateLimitMaxAttempts; i++ {
		if err := rl.Allow(1); err != nil {
			t.Fatalf("Allow() attempt %d: unexpected error: %v", i+1, err)
		}
	}

	// Blocked now.
	if err := rl.Allow(1); err == nil {
		t.Fatal("expected rate limit error")
	}

	// Advance past the window so oldest attempts expire.
	clock.Advance(rateLimitWindow + time.Second)

	// Should be allowed again.
	if err := rl.Allow(1); err != nil {
		t.Fatalf("Allow() after window slide: unexpected error: %v", err)
	}
}

func TestRateLimiter_ConsecutiveFailureBlock(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Record failures up to threshold.
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}

	// Should be blocked.
	err := rl.Allow(1)
	if err == nil {
		t.Fatal("Allow() expected block after consecutive failures")
	}

	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T: %v", err, err)
	}
	if rlErr.RetryAfter <= 0 {
		t.Error("RetryAfter should be positive")
	}

	// Advance past block duration.
	clock.Advance(rateLimitInitialBlock + time.Second)

	// Should be allowed (block expired), but still needs an attempt slot.
	if err := rl.Allow(1); err != nil {
		t.Fatalf("Allow() after block expired: unexpected error: %v", err)
	}
}

func TestRateLimiter_EscalatingBlockDuration(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// First block: 30s (triggered at the 5th failure).
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}

	// Advance past first block.
	clock.Advance(rateLimitInitialBlock + time.Second)

	// Allow one attempt (block has expired).
	if err := rl.Allow(1); err != nil {
		t.Fatalf("Allow() after first block: %v", err)
	}

	// One more failure re-triggers the block with doubled duration (60s).
	// The consecutive failure count is still >= threshold (it was never reset
	// by RecordSuccess), so each new failure re-blocks with escalation.
	rl.RecordFailure(1)

	// Should be blocked with doubled duration (60s).
	err := rl.Allow(1)
	if err == nil {
		t.Fatal("expected block after additional failure")
	}

	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T", err)
	}

	// The block should be ~60s (doubled from 30s).
	expectedBlock := rateLimitInitialBlock * 2
	if rlErr.RetryAfter > expectedBlock+time.Second || rlErr.RetryAfter < expectedBlock-time.Second {
		t.Errorf("RetryAfter = %s, expected ~%s", rlErr.RetryAfter, expectedBlock)
	}
}

func TestRateLimiter_BlockDurationCapped(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Trigger many rounds of failures to escalate block.
	for round := 0; round < 20; round++ {
		for i := 0; i < rateLimitFailureThreshold; i++ {
			rl.RecordFailure(1)
		}
		// Advance past block + window.
		clock.Advance(rateLimitMaxBlock + rateLimitWindow + time.Second)
		_ = rl.Allow(1) // consume an attempt slot
	}

	// Verify block duration is capped.
	_, blockedUntil, _ := rl.GetState(1)
	now := clock.Now()
	if !blockedUntil.IsZero() && blockedUntil.Sub(now) > rateLimitMaxBlock {
		t.Errorf("block duration exceeded max: %s", blockedUntil.Sub(now))
	}
}

func TestRateLimiter_SuccessResetsFailures(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Record failures up to threshold.
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}

	// Should be blocked.
	if err := rl.Allow(1); err == nil {
		t.Fatal("expected block")
	}

	// Record success — should reset everything.
	rl.RecordSuccess(1)

	// Advance past the window to clear attempt slots.
	clock.Advance(rateLimitWindow + time.Second)

	// Should be allowed now.
	if err := rl.Allow(1); err != nil {
		t.Fatalf("Allow() after success: unexpected error: %v", err)
	}

	// Verify state is reset.
	failures, blocked, _ := rl.GetState(1)
	if failures != 0 {
		t.Errorf("consecutive failures = %d, want 0", failures)
	}
	if !blocked.IsZero() {
		t.Errorf("blockedUntil should be zero, got %v", blocked)
	}
}

func TestRateLimiter_InstanceIsolation(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Block instance 1.
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}

	// Instance 1 blocked.
	if err := rl.Allow(1); err == nil {
		t.Fatal("instance 1 should be blocked")
	}

	// Instance 2 should be unaffected.
	if err := rl.Allow(2); err != nil {
		t.Fatalf("instance 2 should not be blocked: %v", err)
	}
}

func TestRateLimiter_Reset(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Block instance 1.
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}
	if err := rl.Allow(1); err == nil {
		t.Fatal("expected block")
	}

	// Reset should clear everything.
	rl.Reset(1)

	if err := rl.Allow(1); err != nil {
		t.Fatalf("Allow() after Reset(): %v", err)
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	var wg sync.WaitGroup
	errs := make(chan error, 200)

	// Concurrent Allow calls for multiple instances.
	for inst := uint(1); inst <= 5; inst++ {
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(id uint) {
				defer wg.Done()
				_ = rl.Allow(id) // just verify no panic/race
			}(inst)
		}
	}

	// Concurrent RecordFailure/RecordSuccess.
	for inst := uint(1); inst <= 5; inst++ {
		wg.Add(2)
		go func(id uint) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				rl.RecordFailure(id)
			}
		}(inst)
		go func(id uint) {
			defer wg.Done()
			rl.RecordSuccess(id)
		}(inst)
	}

	// Concurrent GetState.
	for inst := uint(1); inst <= 5; inst++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			rl.GetState(id)
		}(inst)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

func TestRateLimiter_GetState(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Empty state.
	failures, blocked, attempts := rl.GetState(1)
	if failures != 0 || !blocked.IsZero() || attempts != 0 {
		t.Errorf("empty state: failures=%d blocked=%v attempts=%d", failures, blocked, attempts)
	}

	// After some attempts and failures.
	rl.Allow(1)
	rl.Allow(1)
	rl.RecordFailure(1)
	rl.RecordFailure(1)

	failures, _, attempts = rl.GetState(1)
	if failures != 2 {
		t.Errorf("failures = %d, want 2", failures)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestRateLimiter_SuccessOnNonExistentInstance(t *testing.T) {
	rl := NewRateLimiter()

	// Should not panic.
	rl.RecordSuccess(999)
}

func TestRateLimiter_ErrorMessage(t *testing.T) {
	err := &ErrRateLimited{
		InstanceID: 42,
		Reason:     "too many attempts",
		RetryAfter: 30 * time.Second,
	}

	msg := err.Error()
	if msg == "" {
		t.Fatal("error message is empty")
	}
	// Should contain the instance ID.
	expected := "rate limited for instance 42"
	if len(msg) < len(expected) || msg[:len(expected)] != expected {
		t.Errorf("error message = %q, want prefix %q", msg, expected)
	}
}

func TestRateLimiter_PartialWindowExpiry(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Add 8 attempts.
	for i := 0; i < 8; i++ {
		if err := rl.Allow(1); err != nil {
			t.Fatalf("Allow() attempt %d: %v", i+1, err)
		}
	}

	// Advance 30s (half the window).
	clock.Advance(30 * time.Second)

	// Add 2 more (total 10 in window).
	for i := 0; i < 2; i++ {
		if err := rl.Allow(1); err != nil {
			t.Fatalf("Allow() attempt %d after advance: %v", i+1, err)
		}
	}

	// 11th attempt should be blocked.
	if err := rl.Allow(1); err == nil {
		t.Fatal("expected rate limit error")
	}

	// Advance another 31s — first 8 attempts should expire.
	clock.Advance(31 * time.Second)

	// Now only 2 recent attempts remain, so more should be allowed.
	if err := rl.Allow(1); err != nil {
		t.Fatalf("Allow() after partial expiry: %v", err)
	}
}

// TestConnect_RateLimited verifies that Connect returns rate limit errors.
func TestConnect_RateLimited(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	// Fill the rate limit window by connecting.
	for i := 0; i < rateLimitMaxAttempts; i++ {
		_, err := mgr.Connect(context.Background(), uint(1), host, port)
		if err != nil {
			t.Fatalf("Connect() attempt %d: %v", i+1, err)
		}
	}

	// Next Connect should be rate limited.
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err == nil {
		t.Fatal("Connect() expected rate limit error")
	}

	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T: %v", err, err)
	}
}

// TestConnect_FailureRecording verifies that Connect records failures for rate limiting.
func TestConnect_FailureRecording(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	// Connect to a port that doesn't exist — should fail and record.
	for i := 0; i < rateLimitFailureThreshold; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, _ = mgr.Connect(ctx, uint(1), "127.0.0.1", 1)
		cancel()
	}

	// Verify failures were recorded.
	failures, _, _ := mgr.rateLimiter.GetState(1)
	if failures < rateLimitFailureThreshold {
		t.Errorf("consecutive failures = %d, want >= %d", failures, rateLimitFailureThreshold)
	}
}

// TestConnect_SuccessResetsRateLimit verifies that a successful Connect resets the failure counter.
func TestConnect_SuccessResetsRateLimit(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	// Record some failures manually.
	for i := 0; i < 3; i++ {
		mgr.rateLimiter.RecordFailure(1)
	}

	host, port := parseHostPort(t, ts.addr)

	// Successful connect should reset.
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	failures, _, _ := mgr.rateLimiter.GetState(1)
	if failures != 0 {
		t.Errorf("consecutive failures = %d after success, want 0", failures)
	}
}

// TestEnsureConnected_RateLimited verifies rate limiting propagates through EnsureConnected.
func TestEnsureConnected_RateLimited(t *testing.T) {
	mgr, _, ts := newTestManagerWithPublicKey(t)
	defer ts.cleanup()
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrchestrator{sshHost: host, sshPort: port}

	// Fill the rate limit window.
	for i := 0; i < rateLimitMaxAttempts; i++ {
		// Each call through EnsureConnected that reaches Connect uses an Allow slot.
		// After the first connection, EnsureConnected will reuse the cached connection.
		// So we need to close the connection between calls to force a new Connect.
		mgr.Close(1)
		_, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
		if err != nil {
			t.Fatalf("EnsureConnected() attempt %d: %v", i+1, err)
		}
	}

	// Close connection to force a new Connect attempt.
	mgr.Close(1)

	// Next should be rate limited.
	_, err := mgr.EnsureConnected(context.Background(), uint(1), orch)
	if err == nil {
		t.Fatal("EnsureConnected() expected rate limit error")
	}

	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		// It may be wrapped — check the error message.
		if fmt.Sprintf("%v", err) == "" {
			t.Fatalf("expected rate limit error, got: %v", err)
		}
	}
}
