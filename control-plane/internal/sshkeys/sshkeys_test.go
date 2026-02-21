package sshkeys

import (
	"crypto/ed25519"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateKeyPair(t *testing.T) {
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Validate public key
	if len(pubKey) == 0 {
		t.Fatal("public key is empty")
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(pubKey)
	if err != nil {
		t.Fatalf("public key is not valid authorized_keys format: %v", err)
	}
	if parsed.Type() != "ssh-ed25519" {
		t.Errorf("expected key type ssh-ed25519, got %s", parsed.Type())
	}

	// Validate private key is valid PEM
	if len(privKey) == 0 {
		t.Fatal("private key is empty")
	}
	block, _ := pem.Decode(privKey)
	if block == nil {
		t.Fatal("private key is not valid PEM")
	}

	// Validate private key can be parsed back
	signer, err := ssh.ParsePrivateKey(privKey)
	if err != nil {
		t.Fatalf("private key cannot be parsed: %v", err)
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("parsed private key type: got %s, want ssh-ed25519", signer.PublicKey().Type())
	}
}

func TestGenerateKeyPairUniqueness(t *testing.T) {
	pub1, priv1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("first GenerateKeyPair() error: %v", err)
	}
	pub2, priv2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("second GenerateKeyPair() error: %v", err)
	}

	if string(pub1) == string(pub2) {
		t.Error("two generated key pairs have identical public keys")
	}
	if string(priv1) == string(priv2) {
		t.Error("two generated key pairs have identical private keys")
	}
}

func TestGenerateKeyPairMatchingPair(t *testing.T) {
	pubKeyBytes, privKeyBytes, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Parse the private key to get its public key
	signer, err := ssh.ParsePrivateKey(privKeyBytes)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	// Parse the public key from the authorized_keys format
	parsedPub, _, _, _, err := ssh.ParseAuthorizedKey(pubKeyBytes)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}

	// The public key derived from the private key should match
	derivedPub := signer.PublicKey()
	if string(derivedPub.Marshal()) != string(parsedPub.Marshal()) {
		t.Error("public key from GenerateKeyPair does not match public key derived from private key")
	}
}

func TestSavePrivateKey(t *testing.T) {
	_, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	tmpDir := t.TempDir()
	keyPath, err := SavePrivateKeyToDir(tmpDir, "bot-test-instance", privKey)
	if err != nil {
		t.Fatalf("SavePrivateKeyToDir() error: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, "bot-test-instance.key")
	if keyPath != expectedPath {
		t.Errorf("key path: got %q, want %q", keyPath, expectedPath)
	}

	// Verify file exists and has correct permissions
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("key file permissions: got %o, want 0600", perm)
	}

	// Verify content
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if string(data) != string(privKey) {
		t.Error("saved key content does not match original")
	}
}

func TestSavePrivateKeyCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "dir")

	_, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	_, err = SavePrivateKeyToDir(nestedDir, "bot-nested", privKey)
	if err != nil {
		t.Fatalf("SavePrivateKeyToDir() with nested dir error: %v", err)
	}

	// Verify directory was created with correct permissions
	info, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("stat nested dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("nested path is not a directory")
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("directory permissions: got %o, want 0700", perm)
	}
}

func TestSavePrivateKeyEmptyName(t *testing.T) {
	_, err := SavePrivateKeyToDir(t.TempDir(), "", []byte("key"))
	if err == nil {
		t.Fatal("expected error for empty instance name, got nil")
	}
	if !strings.Contains(err.Error(), "instance name is empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadPrivateKey(t *testing.T) {
	_, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	tmpDir := t.TempDir()
	keyPath, err := SavePrivateKeyToDir(tmpDir, "bot-load-test", privKey)
	if err != nil {
		t.Fatalf("SavePrivateKeyToDir() error: %v", err)
	}

	loaded, err := LoadPrivateKey(keyPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey() error: %v", err)
	}

	if string(loaded) != string(privKey) {
		t.Error("loaded key does not match saved key")
	}

	// Verify the loaded key can be parsed
	_, err = ssh.ParsePrivateKey(loaded)
	if err != nil {
		t.Fatalf("loaded key cannot be parsed: %v", err)
	}
}

func TestLoadPrivateKeyEmptyPath(t *testing.T) {
	_, err := LoadPrivateKey("")
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
	if !strings.Contains(err.Error(), "key path is empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestLoadPrivateKeyNotFound(t *testing.T) {
	_, err := LoadPrivateKey("/nonexistent/path/key.pem")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadPrivateKeyInvalidPEM(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "invalid.key")
	if err := os.WriteFile(tmpFile, []byte("not a pem file"), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	_, err := LoadPrivateKey(tmpFile)
	if err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}
	if !strings.Contains(err.Error(), "valid PEM block") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestFormatPublicKeyForAuthorizedKeys(t *testing.T) {
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	formatted, err := FormatPublicKeyForAuthorizedKeys(pubKey)
	if err != nil {
		t.Fatalf("FormatPublicKeyForAuthorizedKeys() error: %v", err)
	}

	// Should start with ssh-ed25519
	if !strings.HasPrefix(formatted, "ssh-ed25519 ") {
		t.Errorf("formatted key does not start with 'ssh-ed25519 ': %q", formatted)
	}

	// Should end with a newline
	if !strings.HasSuffix(formatted, "\n") {
		t.Error("formatted key does not end with newline")
	}

	// Should have exactly one newline (at the end)
	if strings.Count(formatted, "\n") != 1 {
		t.Errorf("formatted key has %d newlines, expected 1", strings.Count(formatted, "\n"))
	}

	// Should be parseable as an authorized key
	_, _, _, _, err = ssh.ParseAuthorizedKey([]byte(formatted))
	if err != nil {
		t.Fatalf("formatted key is not valid authorized_keys format: %v", err)
	}
}

func TestFormatPublicKeyForAuthorizedKeysEmpty(t *testing.T) {
	_, err := FormatPublicKeyForAuthorizedKeys(nil)
	if err == nil {
		t.Fatal("expected error for nil key, got nil")
	}

	_, err = FormatPublicKeyForAuthorizedKeys([]byte{})
	if err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

func TestFormatPublicKeyForAuthorizedKeysInvalid(t *testing.T) {
	_, err := FormatPublicKeyForAuthorizedKeys([]byte("not a valid ssh key"))
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
	if !strings.Contains(err.Error(), "invalid SSH public key") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDeletePrivateKey(t *testing.T) {
	_, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	tmpDir := t.TempDir()
	keyPath, err := SavePrivateKeyToDir(tmpDir, "bot-delete-test", privKey)
	if err != nil {
		t.Fatalf("SavePrivateKeyToDir() error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file should exist: %v", err)
	}

	// Delete
	if err := DeletePrivateKey(keyPath); err != nil {
		t.Fatalf("DeletePrivateKey() error: %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Error("key file should not exist after deletion")
	}
}

func TestDeletePrivateKeyEmpty(t *testing.T) {
	// Empty path should be a no-op, not an error
	if err := DeletePrivateKey(""); err != nil {
		t.Fatalf("DeletePrivateKey(\"\") should not error: %v", err)
	}
}

func TestDeletePrivateKeyNotFound(t *testing.T) {
	// Deleting a nonexistent file should not error
	if err := DeletePrivateKey("/nonexistent/path/key.pem"); err != nil {
		t.Fatalf("DeletePrivateKey for nonexistent file should not error: %v", err)
	}
}

func TestEndToEndKeyWorkflow(t *testing.T) {
	// Generate
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Save
	tmpDir := t.TempDir()
	keyPath, err := SavePrivateKeyToDir(tmpDir, "bot-e2e-test", privKey)
	if err != nil {
		t.Fatalf("SavePrivateKeyToDir() error: %v", err)
	}

	// Load
	loaded, err := LoadPrivateKey(keyPath)
	if err != nil {
		t.Fatalf("LoadPrivateKey() error: %v", err)
	}

	// Format public key
	formatted, err := FormatPublicKeyForAuthorizedKeys(pubKey)
	if err != nil {
		t.Fatalf("FormatPublicKeyForAuthorizedKeys() error: %v", err)
	}

	// Verify the loaded private key can sign, and the public key can verify
	signer, err := ssh.ParsePrivateKey(loaded)
	if err != nil {
		t.Fatalf("parse loaded private key: %v", err)
	}

	parsedPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(formatted))
	if err != nil {
		t.Fatalf("parse formatted public key: %v", err)
	}

	// Sign some data with the private key
	testData := []byte("test data for signing")
	sig, err := signer.Sign(nil, testData)
	if err != nil {
		t.Fatalf("sign test data: %v", err)
	}

	// Verify with the public key
	err = parsedPub.Verify(testData, sig)
	if err != nil {
		t.Fatalf("verify signature with public key: %v", err)
	}

	// Verify the key is ED25519
	cryptoPub := parsedPub.(ssh.CryptoPublicKey).CryptoPublicKey()
	if _, ok := cryptoPub.(ed25519.PublicKey); !ok {
		t.Errorf("expected ed25519.PublicKey, got %T", cryptoPub)
	}

	// Clean up
	if err := DeletePrivateKey(keyPath); err != nil {
		t.Fatalf("DeletePrivateKey() error: %v", err)
	}
}
