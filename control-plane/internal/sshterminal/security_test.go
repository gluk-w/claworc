package sshterminal

import (
	"context"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// --- Shell validation tests ---

func TestValidateShell_AllowedShells(t *testing.T) {
	for _, shell := range AllowedShells {
		if err := ValidateShell(shell); err != nil {
			t.Errorf("ValidateShell(%q) returned error: %v", shell, err)
		}
	}
}

func TestValidateShell_EmptyString(t *testing.T) {
	if err := ValidateShell(""); err != nil {
		t.Errorf("ValidateShell(\"\") should allow empty (defaults to DefaultShell), got: %v", err)
	}
}

func TestValidateShell_DisallowedShells(t *testing.T) {
	disallowed := []string{
		"/usr/bin/python3",
		"/bin/bash; rm -rf /",
		"bash",
		"/bin/bash\n/bin/sh",
		"/bin/bash\x00/bin/sh",
		"../../bin/bash",
		"/tmp/evil",
		"/bin/bash --norc",
		"$(whoami)",
		"`whoami`",
	}
	for _, shell := range disallowed {
		if err := ValidateShell(shell); err == nil {
			t.Errorf("ValidateShell(%q) should return error for disallowed shell", shell)
		}
	}
}

func TestCreateInteractiveSession_RejectsInvalidShell(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Close()
		},
	})
	defer cleanup()

	_, err := CreateInteractiveSession(client, "/usr/bin/python3")
	if err == nil {
		t.Fatal("expected error for disallowed shell, got nil")
	}
}

func TestCreateInteractiveSession_AcceptsAllowedShell(t *testing.T) {
	shellStarted := make(chan struct{})

	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			close(shellStarted)
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	session, err := CreateInteractiveSession(client, "/bin/sh")
	if err != nil {
		t.Fatalf("CreateInteractiveSession(/bin/sh) failed: %v", err)
	}
	defer session.Close()

	select {
	case <-shellStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("shell did not start within timeout")
	}
}

func TestSessionManager_RejectsInvalidShell(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Close()
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	_, err := mgr.CreateSession(context.Background(), client, 1, 1, "/usr/bin/evil")
	if err == nil {
		t.Fatal("expected error for disallowed shell in SessionManager.CreateSession")
	}
}

// --- Rate limiter tests ---

func TestRateLimiter_AllowsWithinBurst(t *testing.T) {
	rl := NewRateLimiter(10, 5) // 10/sec, burst 5

	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Fatalf("expected Allow() to return true at call %d (within burst)", i)
		}
	}
}

func TestRateLimiter_RejectsOverBurst(t *testing.T) {
	rl := NewRateLimiter(10, 3) // 10/sec, burst 3

	// Exhaust the burst
	for i := 0; i < 3; i++ {
		rl.Allow()
	}

	// Next call should be rejected
	if rl.Allow() {
		t.Fatal("expected Allow() to return false after burst exhausted")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl := NewRateLimiter(100, 1) // 100/sec, burst 1

	// Use the one token
	if !rl.Allow() {
		t.Fatal("first call should succeed")
	}

	// Should be denied immediately
	if rl.Allow() {
		t.Fatal("second call should be denied")
	}

	// Wait for refill (at 100/sec, ~10ms per token)
	time.Sleep(50 * time.Millisecond)

	// Should succeed after refill
	if !rl.Allow() {
		t.Fatal("call after refill should succeed")
	}
}

func TestRateLimiter_TokensCappedAtMax(t *testing.T) {
	rl := NewRateLimiter(1000, 5)

	// Wait a bit to accumulate tokens
	time.Sleep(100 * time.Millisecond)

	// Should have max 5 tokens even after waiting
	allowed := 0
	for i := 0; i < 10; i++ {
		if rl.Allow() {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("expected exactly 5 tokens (burst cap), got %d", allowed)
	}
}

// --- Security constants tests ---

func TestSecurityConstants(t *testing.T) {
	if MaxInputMessageSize <= 0 {
		t.Error("MaxInputMessageSize must be positive")
	}
	if MaxTermCols <= 0 || MaxTermCols > 10000 {
		t.Error("MaxTermCols must be reasonable")
	}
	if MaxTermRows <= 0 || MaxTermRows > 10000 {
		t.Error("MaxTermRows must be reasonable")
	}
	if MessageRateLimit <= 0 {
		t.Error("MessageRateLimit must be positive")
	}
	if MessageRateBurst <= 0 {
		t.Error("MessageRateBurst must be positive")
	}
}

// --- Default idle timeout tests ---

func TestSessionManager_DefaultIdleTimeout(t *testing.T) {
	mgr := NewSessionManager()
	if mgr.IdleTimeout != DefaultIdleTimeout {
		t.Errorf("expected default idle timeout %v, got %v", DefaultIdleTimeout, mgr.IdleTimeout)
	}
	if mgr.IdleTimeout <= 0 {
		t.Error("default idle timeout should be positive")
	}
}
