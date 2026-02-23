package orchestrator

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// mockExecFunc creates a mock ExecFunc that records calls and returns preset responses.
type execCall struct {
	Name string
	Cmd  []string
}

func newMockExec(responses []struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}) (ExecFunc, *[]execCall) {
	calls := &[]execCall{}
	idx := 0
	return func(ctx context.Context, name string, cmd []string) (string, string, int, error) {
		*calls = append(*calls, execCall{Name: name, Cmd: cmd})
		if idx < len(responses) {
			r := responses[idx]
			idx++
			return r.stdout, r.stderr, r.exitCode, r.err
		}
		return "", "", 0, nil
	}, calls
}

func TestConfigureSSHAccess_Success(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"

	execFn, calls := newMockExec([]struct {
		stdout   string
		stderr   string
		exitCode int
		err      error
	}{
		{"", "", 0, nil}, // mkdir -p /root/.ssh && chmod 700 /root/.ssh
		{"", "", 0, nil}, // write authorized_keys
	})

	err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(*calls) != 2 {
		t.Fatalf("expected 2 exec calls, got %d", len(*calls))
	}

	// Verify first call: mkdir + chmod
	call0 := (*calls)[0]
	if call0.Name != "test-instance" {
		t.Errorf("call 0: expected instance name 'test-instance', got %q", call0.Name)
	}
	if len(call0.Cmd) < 3 || !strings.Contains(call0.Cmd[2], "mkdir -p /root/.ssh") {
		t.Errorf("call 0: expected mkdir command, got %v", call0.Cmd)
	}

	// Verify second call: base64 decode + write authorized_keys + chmod
	call1 := (*calls)[1]
	if call1.Name != "test-instance" {
		t.Errorf("call 1: expected instance name 'test-instance', got %q", call1.Name)
	}
	if len(call1.Cmd) < 3 {
		t.Fatalf("call 1: expected at least 3 args, got %v", call1.Cmd)
	}
	cmdStr := call1.Cmd[2]
	if !strings.Contains(cmdStr, "base64 -d") {
		t.Errorf("call 1: expected base64 decode in command, got %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "/root/.ssh/authorized_keys") {
		t.Errorf("call 1: expected authorized_keys path, got %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "chmod 600") {
		t.Errorf("call 1: expected chmod 600, got %q", cmdStr)
	}

	// Verify the base64 content actually decodes to the public key
	b64Expected := base64.StdEncoding.EncodeToString([]byte(publicKey))
	if !strings.Contains(cmdStr, b64Expected) {
		t.Errorf("call 1: expected base64 encoded key in command")
	}
}

func TestConfigureSSHAccess_PermissionVerification(t *testing.T) {
	// Verify that the commands set correct permissions:
	// /root/.ssh should be 700, authorized_keys should be 600
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"

	execFn, calls := newMockExec([]struct {
		stdout   string
		stderr   string
		exitCode int
		err      error
	}{
		{"", "", 0, nil},
		{"", "", 0, nil},
	})

	err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check directory permissions (chmod 700)
	mkdirCmd := (*calls)[0].Cmd[2]
	if !strings.Contains(mkdirCmd, "chmod 700 /root/.ssh") {
		t.Errorf("expected chmod 700 for .ssh dir, got %q", mkdirCmd)
	}

	// Check file permissions (chmod 600)
	writeCmd := (*calls)[1].Cmd[2]
	if !strings.Contains(writeCmd, "chmod 600 /root/.ssh/authorized_keys") {
		t.Errorf("expected chmod 600 for authorized_keys, got %q", writeCmd)
	}
}

func TestConfigureSSHAccess_Idempotency(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"
	callCount := 0

	execFn := func(ctx context.Context, name string, cmd []string) (string, string, int, error) {
		callCount++
		return "", "", 0, nil
	}

	// Call twice with the same key - both should succeed
	for i := 0; i < 2; i++ {
		err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	// Each call makes 2 exec calls (mkdir + write), so 4 total
	if callCount != 4 {
		t.Errorf("expected 4 exec calls for 2 invocations, got %d", callCount)
	}
}

func TestConfigureSSHAccess_MkdirError(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"

	execFn, _ := newMockExec([]struct {
		stdout   string
		stderr   string
		exitCode int
		err      error
	}{
		{"", "", 0, fmt.Errorf("container not running")}, // mkdir fails
	})

	err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create .ssh directory") {
		t.Errorf("expected 'create .ssh directory' in error, got %q", err.Error())
	}
}

func TestConfigureSSHAccess_MkdirNonZeroExit(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"

	execFn, _ := newMockExec([]struct {
		stdout   string
		stderr   string
		exitCode int
		err      error
	}{
		{"", "permission denied", 1, nil}, // mkdir exits non-zero
	})

	err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected 'permission denied' in error, got %q", err.Error())
	}
}

func TestConfigureSSHAccess_WriteError(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"

	execFn, _ := newMockExec([]struct {
		stdout   string
		stderr   string
		exitCode int
		err      error
	}{
		{"", "", 0, nil}, // mkdir succeeds
		{"", "", 0, fmt.Errorf("container stopped suddenly")}, // write fails
	})

	err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "write authorized_keys") {
		t.Errorf("expected 'write authorized_keys' in error, got %q", err.Error())
	}
}

func TestConfigureSSHAccess_WriteNonZeroExit(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"

	execFn, _ := newMockExec([]struct {
		stdout   string
		stderr   string
		exitCode int
		err      error
	}{
		{"", "", 0, nil},          // mkdir succeeds
		{"", "disk full", 1, nil}, // write exits non-zero
	})

	err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("expected 'disk full' in error, got %q", err.Error())
	}
}

func TestConfigureSSHAccess_StoppedContainer(t *testing.T) {
	publicKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc"

	execFn, _ := newMockExec([]struct {
		stdout   string
		stderr   string
		exitCode int
		err      error
	}{
		{"", "", -1, fmt.Errorf("container test-instance is not running")},
	})

	err := configureSSHAccess(context.Background(), execFn, "test-instance", publicKey)
	if err == nil {
		t.Fatal("expected error for stopped container, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' in error, got %q", err.Error())
	}
}
