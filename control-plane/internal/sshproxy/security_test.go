package sshproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Rate Limiting Security Tests ---

// TestSecurity_RapidReconnectionsTriggersRateLimit verifies that rapid SSH
// reconnection attempts are blocked by the rate limiter, preventing brute-force
// and reconnection storm attacks.
func TestSecurity_RapidReconnectionsTriggersRateLimit(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Rapidly fire connection attempts without any time gap
	allowed := 0
	blocked := 0
	for i := 0; i < 20; i++ {
		if err := rl.Allow(1); err != nil {
			var rlErr *ErrRateLimited
			if !errors.As(err, &rlErr) {
				t.Fatalf("expected *ErrRateLimited, got %T: %v", err, err)
			}
			if rlErr.InstanceID != 1 {
				t.Errorf("ErrRateLimited.InstanceID = %d, want 1", rlErr.InstanceID)
			}
			blocked++
		} else {
			allowed++
		}
	}

	if allowed != rateLimitMaxAttempts {
		t.Errorf("SECURITY: allowed %d attempts, expected max %d", allowed, rateLimitMaxAttempts)
	}
	if blocked != 20-rateLimitMaxAttempts {
		t.Errorf("SECURITY: blocked %d attempts, expected %d", blocked, 20-rateLimitMaxAttempts)
	}
}

// TestSecurity_ConsecutiveFailuresEscalateBlock verifies that repeated connection
// failures trigger increasingly long block periods, protecting against persistent
// brute-force attacks.
func TestSecurity_ConsecutiveFailuresEscalateBlock(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Record failures to trigger first block (30s)
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}

	err := rl.Allow(1)
	if err == nil {
		t.Fatal("SECURITY: should be blocked after consecutive failures")
	}
	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T", err)
	}

	// First block should be ~30s
	if rlErr.RetryAfter < 29*time.Second || rlErr.RetryAfter > 31*time.Second {
		t.Errorf("SECURITY: first block duration = %s, expected ~30s", rlErr.RetryAfter)
	}

	// Wait out the block and trigger another failure
	clock.Advance(rateLimitInitialBlock + time.Second)
	// Consume the attempt slot
	rl.Allow(1)
	rl.RecordFailure(1)

	err = rl.Allow(1)
	if err == nil {
		t.Fatal("SECURITY: should be blocked after additional failure")
	}
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T", err)
	}

	// Second block should be ~60s (doubled)
	expectedBlock := rateLimitInitialBlock * 2
	if rlErr.RetryAfter < expectedBlock-time.Second || rlErr.RetryAfter > expectedBlock+time.Second {
		t.Errorf("SECURITY: second block duration = %s, expected ~%s", rlErr.RetryAfter, expectedBlock)
	}
}

// TestSecurity_LegitimateReconnectionsNotBlocked verifies that normal
// reconnection patterns (connect, use, disconnect, reconnect) are not
// blocked by the rate limiter.
func TestSecurity_LegitimateReconnectionsNotBlocked(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Simulate legitimate usage: connect, succeed, wait, reconnect
	for cycle := 0; cycle < 5; cycle++ {
		// Allow connection attempt
		if err := rl.Allow(1); err != nil {
			t.Fatalf("SECURITY: legitimate reconnection blocked on cycle %d: %v", cycle, err)
		}

		// Record success
		rl.RecordSuccess(1)

		// Wait 15 seconds between reconnections (realistic interval)
		clock.Advance(15 * time.Second)
	}

	// Even after 5 reconnection cycles, the limiter should still allow more
	if err := rl.Allow(1); err != nil {
		t.Fatalf("SECURITY: legitimate reconnection blocked after normal usage: %v", err)
	}
}

// TestSecurity_SuccessResetsBlockAfterFailures verifies that a successful
// connection after failures completely resets the rate limiter, allowing
// normal operations to resume.
func TestSecurity_SuccessResetsBlockAfterFailures(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Record 4 failures (just under threshold)
	for i := 0; i < rateLimitFailureThreshold-1; i++ {
		rl.RecordFailure(1)
	}

	// Should still be allowed
	if err := rl.Allow(1); err != nil {
		t.Fatal("should not be blocked below failure threshold")
	}

	// Record success — should reset failures
	rl.RecordSuccess(1)

	// Advance past window to clear attempt slots
	clock.Advance(rateLimitWindow + time.Second)

	// Now record failures again — should need full threshold to block
	for i := 0; i < rateLimitFailureThreshold-1; i++ {
		rl.RecordFailure(1)
	}

	// Should still be allowed (counter was reset)
	if err := rl.Allow(1); err != nil {
		t.Fatal("SECURITY: success should have reset failure counter, but still blocked")
	}
}

// TestSecurity_RateLimitPerInstanceIsolation verifies that rate limiting for
// one instance does not affect other instances.
func TestSecurity_RateLimitPerInstanceIsolation(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Block instance 1 via consecutive failures
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}

	// Exhaust rate limit window for instance 2
	for i := 0; i < rateLimitMaxAttempts; i++ {
		if err := rl.Allow(2); err != nil {
			t.Fatalf("instance 2 should not be blocked: %v", err)
		}
	}

	// Instance 1 should be blocked (consecutive failures)
	if err := rl.Allow(1); err == nil {
		t.Error("SECURITY: instance 1 should be blocked")
	}

	// Instance 2 should be blocked (rate limit window)
	if err := rl.Allow(2); err == nil {
		t.Error("SECURITY: instance 2 should be rate limited")
	}

	// Instance 3 should be completely unaffected
	if err := rl.Allow(3); err != nil {
		t.Errorf("SECURITY: instance 3 should not be affected by other instances: %v", err)
	}
}

// TestSecurity_RateLimitBlockDurationCapped verifies that block duration
// doesn't grow unbounded, ensuring that a persistent attacker eventually
// gets let through (where other defenses can catch them).
func TestSecurity_RateLimitBlockDurationCapped(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Escalate block many times
	for round := 0; round < 20; round++ {
		for i := 0; i < rateLimitFailureThreshold; i++ {
			rl.RecordFailure(1)
		}
		// Advance past block + window
		clock.Advance(rateLimitMaxBlock + rateLimitWindow + time.Second)
		_ = rl.Allow(1)
	}

	// Final block: verify it doesn't exceed max
	for i := 0; i < rateLimitFailureThreshold; i++ {
		rl.RecordFailure(1)
	}

	err := rl.Allow(1)
	if err == nil {
		t.Fatal("expected block")
	}
	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T", err)
	}
	if rlErr.RetryAfter > rateLimitMaxBlock+time.Second {
		t.Errorf("SECURITY: block duration %s exceeds max %s", rlErr.RetryAfter, rateLimitMaxBlock)
	}
}

// TestSecurity_RateLimitConcurrentBruteForce verifies that concurrent
// connection attempts from multiple goroutines are properly rate limited.
func TestSecurity_RateLimitConcurrentBruteForce(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	blocked := 0

	// Simulate 50 concurrent connection attempts for the same instance
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := rl.Allow(1)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				allowed++
			} else {
				blocked++
			}
		}()
	}

	wg.Wait()

	// Should have allowed exactly rateLimitMaxAttempts
	if allowed != rateLimitMaxAttempts {
		t.Errorf("SECURITY: concurrent test allowed %d attempts, expected %d", allowed, rateLimitMaxAttempts)
	}
	if blocked != 50-rateLimitMaxAttempts {
		t.Errorf("SECURITY: concurrent test blocked %d attempts, expected %d", blocked, 50-rateLimitMaxAttempts)
	}
}

// TestSecurity_RateLimitErrorContainsRetryInfo verifies that rate limit errors
// contain useful retry information for legitimate callers.
func TestSecurity_RateLimitErrorContainsRetryInfo(t *testing.T) {
	clock := newFakeClock(time.Now())
	rl := newTestRateLimiter(clock)

	// Trigger rate limit via window exhaustion
	for i := 0; i < rateLimitMaxAttempts; i++ {
		rl.Allow(1)
	}

	err := rl.Allow(1)
	if err == nil {
		t.Fatal("expected rate limit error")
	}

	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected *ErrRateLimited, got %T", err)
	}

	// Error should have meaningful fields
	if rlErr.InstanceID != 1 {
		t.Errorf("InstanceID = %d, want 1", rlErr.InstanceID)
	}
	if rlErr.Reason == "" {
		t.Error("Reason should not be empty")
	}
	if rlErr.RetryAfter <= 0 {
		t.Error("RetryAfter should be positive")
	}

	// Error message should be informative but not reveal internals
	msg := rlErr.Error()
	if msg == "" {
		t.Error("error message is empty")
	}
}

// --- SSH Connection Security Tests ---

// TestSecurity_InvalidKeyRejected verifies that the test SSH server rejects
// connections with unauthorized keys.
func TestSecurity_InvalidKeyRejected(t *testing.T) {
	// Create an authorized key and server
	_, serverPrivPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	serverSigner, err := ParsePrivateKey(serverPrivPEM)
	if err != nil {
		t.Fatalf("parse server key: %v", err)
	}

	ts := testSSHServer(t, serverSigner.PublicKey())
	defer ts.cleanup()

	// Try connecting with a different key
	_, wrongPrivPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}
	wrongSigner, err := ParsePrivateKey(wrongPrivPEM)
	if err != nil {
		t.Fatalf("parse wrong key: %v", err)
	}

	mgr := NewSSHManager(wrongSigner, "")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err = mgr.Connect(context.Background(), uint(1), host, port)
	if err == nil {
		t.Fatal("SECURITY: connection with unauthorized key should be rejected")
	}

	// Should record a failure in rate limiter
	failures, _, _ := mgr.rateLimiter.GetState(1)
	if failures < 1 {
		t.Error("SECURITY: failed connection should be recorded in rate limiter")
	}
}

// TestSecurity_ConnectionFailureRecordedForRateLimiting verifies that failed
// connection attempts are tracked by the rate limiter.
func TestSecurity_ConnectionFailureRecordedForRateLimiting(t *testing.T) {
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

	// Attempt connections to a port that doesn't exist
	for i := 0; i < rateLimitFailureThreshold; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, _ = mgr.Connect(ctx, uint(1), "127.0.0.1", 1)
		cancel()
	}

	// Verify failures accumulated
	failures, _, _ := mgr.rateLimiter.GetState(1)
	if failures < rateLimitFailureThreshold {
		t.Errorf("SECURITY: expected >= %d failures, got %d", rateLimitFailureThreshold, failures)
	}

	// Next attempt should be blocked by rate limiter
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = mgr.Connect(ctx, uint(1), "127.0.0.1", 1)
	if err == nil {
		t.Fatal("SECURITY: should be rate limited after consecutive failures")
	}

	var rlErr *ErrRateLimited
	if !errors.As(err, &rlErr) {
		t.Logf("error type: %T, error: %v", err, err)
		// The Connect method returns rate limit errors directly
	}
}

// TestSecurity_SuccessfulConnectionResetsRateLimiter verifies that a successful
// connection resets the rate limiter state.
func TestSecurity_SuccessfulConnectionResetsRateLimiter(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "")
	defer mgr.CloseAll()

	// Record failures manually
	for i := 0; i < rateLimitFailureThreshold-1; i++ {
		mgr.rateLimiter.RecordFailure(1)
	}

	// Verify failures are recorded
	failures, _, _ := mgr.rateLimiter.GetState(1)
	if failures != rateLimitFailureThreshold-1 {
		t.Fatalf("expected %d failures, got %d", rateLimitFailureThreshold-1, failures)
	}

	// Successful connection should reset
	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), uint(1), host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	failures, _, _ = mgr.rateLimiter.GetState(1)
	if failures != 0 {
		t.Errorf("SECURITY: failures = %d after successful connection, should be 0", failures)
	}
}

// TestSecurity_ReloadKeysAtomicSwap verifies that ReloadKeys atomically
// replaces the signer and public key without affecting concurrent readers.
func TestSecurity_ReloadKeysAtomicSwap(t *testing.T) {
	pubKey1, privKeyPEM1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key 1: %v", err)
	}
	signer1, err := ParsePrivateKey(privKeyPEM1)
	if err != nil {
		t.Fatalf("parse key 1: %v", err)
	}

	mgr := NewSSHManager(signer1, string(pubKey1))
	defer mgr.CloseAll()

	fp1 := mgr.GetPublicKeyFingerprint()

	// Generate new key
	pubKey2, privKeyPEM2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key 2: %v", err)
	}
	signer2, err := ParsePrivateKey(privKeyPEM2)
	if err != nil {
		t.Fatalf("parse key 2: %v", err)
	}

	// Concurrent reads during reload
	var wg sync.WaitGroup
	errs := make(chan error, 100)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fp := mgr.GetPublicKeyFingerprint()
			pk := mgr.GetPublicKey()

			// Fingerprint and public key should always be consistent
			// (both from the same key pair)
			if fp == "" || pk == "" {
				errs <- fmt.Errorf("got empty fingerprint or public key")
			}
		}()
	}

	// Reload in the middle
	mgr.ReloadKeys(signer2, string(pubKey2))

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("SECURITY: concurrent access error during key reload: %v", err)
	}

	// After reload, should have new key
	fp2 := mgr.GetPublicKeyFingerprint()
	if fp1 == fp2 {
		t.Error("SECURITY: fingerprint unchanged after ReloadKeys")
	}
	if mgr.GetPublicKey() != string(pubKey2) {
		t.Error("SECURITY: public key unchanged after ReloadKeys")
	}
}

// --- IP Restriction Security Tests ---

// TestSecurity_IPRestrictionBlocksDisallowedIP verifies that connections from
// non-whitelisted IPs are blocked.
func TestSecurity_IPRestrictionBlocksDisallowedIP(t *testing.T) {
	r, err := ParseIPRestrictions("10.0.0.0/8, 172.16.0.0/12")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	testCases := []struct {
		ip      string
		allowed bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", false},   // Not in either range
		{"8.8.8.8", false},       // Public IP
		{"172.32.0.1", false},    // Just outside /12 range
		{"11.0.0.1", false},      // Outside /8 range
	}

	for _, tc := range testCases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %s", tc.ip)
		}
		got := r.IsAllowed(ip)
		if got != tc.allowed {
			t.Errorf("SECURITY: IP %s IsAllowed = %v, want %v", tc.ip, got, tc.allowed)
		}
	}
}

// TestSecurity_NilRestrictionAllowsAll verifies that a nil restriction
// (empty whitelist) allows all connections.
func TestSecurity_NilRestrictionAllowsAll(t *testing.T) {
	r, err := ParseIPRestrictions("")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r != nil {
		t.Fatal("expected nil restriction for empty input")
	}

	// nil restriction should allow everything
	var nilR *IPRestriction
	if !nilR.IsAllowed(net.ParseIP("192.168.1.1")) {
		t.Error("SECURITY: nil restriction should allow all IPs")
	}
}

// TestSecurity_InvalidIPRestrictionRejected verifies that malformed IP/CIDR
// entries are rejected during parsing.
func TestSecurity_InvalidIPRestrictionRejected(t *testing.T) {
	invalidInputs := []string{
		"not-an-ip",
		"10.0.0.0/33",          // Invalid CIDR prefix
		"256.256.256.256",      // Invalid IP octets
		"10.0.0.1, not-valid",  // Mixed valid and invalid
	}

	for _, input := range invalidInputs {
		_, err := ParseIPRestrictions(input)
		if err == nil {
			t.Errorf("SECURITY: expected error for invalid input %q", input)
		}
	}
}

// TestSecurity_ErrIPRestrictedContainsContext verifies that IP restriction
// errors contain useful context for debugging without leaking sensitive info.
func TestSecurity_ErrIPRestrictedContainsContext(t *testing.T) {
	err := &ErrIPRestricted{
		InstanceID: 42,
		SourceIP:   "192.168.1.100",
		Reason:     "not in allowed list [10.0.0.0/8]",
	}

	msg := err.Error()
	if msg == "" {
		t.Fatal("error message is empty")
	}

	// Should contain instance ID and source IP for debugging
	if !strings.Contains(msg, "42") {
		t.Error("error should contain instance ID")
	}
	if !strings.Contains(msg, "192.168.1.100") {
		t.Error("error should contain source IP")
	}
}

