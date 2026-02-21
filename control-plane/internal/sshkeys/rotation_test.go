package sshkeys

import (
	"os"
	"strings"
	"testing"
)

func TestRotateKeyPair_NilClient(t *testing.T) {
	_, _, _, err := RotateKeyPair(nil, "bot-test", "ssh-ed25519 AAAA...", "localhost", 22)
	if err == nil {
		t.Fatal("expected error for nil client, got nil")
	}
	if !strings.Contains(err.Error(), "SSH client is nil") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRotateKeyPair_EmptyInstanceName(t *testing.T) {
	// The nil client check comes first, so this will match "SSH client is nil"
	_, _, _, err := RotateKeyPair(nil, "", "ssh-ed25519 AAAA...", "localhost", 22)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRemovePublicKeyFromAgent_InvalidKeyFormat(t *testing.T) {
	err := removePublicKeyFromAgent(nil, "onlyonepart")
	if err == nil {
		t.Fatal("expected error for invalid key format, got nil")
	}
	if !strings.Contains(err.Error(), "invalid public key format") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecuteSSHCommand_NilClient(t *testing.T) {
	err := executeSSHCommand(nil, "echo test")
	if err == nil {
		t.Fatal("expected error for nil client, got nil")
	}
}

func TestDialSSH_InvalidKeyPath(t *testing.T) {
	_, err := dialSSH("/nonexistent/key/path.pem", "localhost", 22)
	if err == nil {
		t.Fatal("expected error for invalid key path, got nil")
	}
	if !strings.Contains(err.Error(), "load private key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDialSSH_InvalidPEMKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := tmpDir + "/bad.key"
	if err := os.WriteFile(keyPath, []byte("not a valid pem"), 0600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	_, err := dialSSH(keyPath, "localhost", 22)
	if err == nil {
		t.Fatal("expected error for invalid PEM key, got nil")
	}
}

func TestDialSSH_ValidKeyUnreachableHost(t *testing.T) {
	_, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpDir := t.TempDir()
	keyPath, err := SavePrivateKeyToDir(tmpDir, "bot-dial-test", privKey)
	if err != nil {
		t.Fatalf("save key: %v", err)
	}

	// Use a non-routable IP to trigger connection failure
	_, err = dialSSH(keyPath, "192.0.2.1", 22)
	if err == nil {
		t.Fatal("expected error for unreachable host, got nil")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTestSSHConnection_InvalidKeyPath(t *testing.T) {
	err := testSSHConnection("/nonexistent/key.pem", "localhost", 22)
	if err == nil {
		t.Fatal("expected error for invalid key path, got nil")
	}
}

func TestRotationResult_Fields(t *testing.T) {
	result := &RotationResult{
		NewFingerprint: "SHA256:abc123",
	}
	if result.NewFingerprint != "SHA256:abc123" {
		t.Errorf("unexpected fingerprint: %s", result.NewFingerprint)
	}
	if !result.RotatedAt.IsZero() {
		t.Error("expected zero RotatedAt for default value")
	}
}
