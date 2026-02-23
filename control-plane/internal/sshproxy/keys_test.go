package sshproxy

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateKeyPair(t *testing.T) {
	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}
	if len(pubKey) == 0 {
		t.Fatal("public key is empty")
	}
	if len(privKey) == 0 {
		t.Fatal("private key is empty")
	}

	// Verify private key is valid PEM
	block, _ := pem.Decode(privKey)
	if block == nil {
		t.Fatal("private key is not valid PEM")
	}
	if block.Type != "PRIVATE KEY" {
		t.Fatalf("expected PEM type PRIVATE KEY, got %s", block.Type)
	}

	// Verify public key is valid OpenSSH format
	_, _, _, _, err = ssh.ParseAuthorizedKey(pubKey)
	if err != nil {
		t.Fatalf("public key is not valid OpenSSH format: %v", err)
	}
}

func TestGenerateKeyPair_Uniqueness(t *testing.T) {
	pub1, priv1, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("first GenerateKeyPair() error: %v", err)
	}
	pub2, priv2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("second GenerateKeyPair() error: %v", err)
	}

	if bytes.Equal(pub1, pub2) {
		t.Error("two generated public keys are identical")
	}
	if bytes.Equal(priv1, priv2) {
		t.Error("two generated private keys are identical")
	}
}

func TestGenerateKeyPair_SignVerify(t *testing.T) {
	pubKeyBytes, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKey() error: %v", err)
	}

	parsedPub, _, _, _, err := ssh.ParseAuthorizedKey(pubKeyBytes)
	if err != nil {
		t.Fatalf("ParseAuthorizedKey() error: %v", err)
	}

	// Sign data with private key
	data := []byte("test message for signing")
	sig, err := signer.Sign(rand.Reader, data)
	if err != nil {
		t.Fatalf("Sign() error: %v", err)
	}

	// Verify signature with public key
	if err := parsedPub.Verify(data, sig); err != nil {
		t.Fatalf("Verify() error: signature does not match public key: %v", err)
	}
}

func TestGenerateKeyPair_KeyType(t *testing.T) {
	pubKeyBytes, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Verify public key type
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(pubKeyBytes)
	if err != nil {
		t.Fatalf("ParseAuthorizedKey() error: %v", err)
	}
	if parsed.Type() != "ssh-ed25519" {
		t.Errorf("public key type: got %s, want ssh-ed25519", parsed.Type())
	}

	// Verify private key is ED25519 via PKCS8 parsing
	block, _ := pem.Decode(privKeyPEM)
	if block == nil {
		t.Fatal("failed to decode PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKCS8PrivateKey() error: %v", err)
	}
	if _, ok := key.(ed25519.PrivateKey); !ok {
		t.Errorf("expected ed25519.PrivateKey, got %T", key)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	if err := SaveKeyPair(dir, privKey, pubKey); err != nil {
		t.Fatalf("SaveKeyPair() error: %v", err)
	}

	// Load and compare private key
	loadedPriv, err := LoadPrivateKey(dir)
	if err != nil {
		t.Fatalf("LoadPrivateKey() error: %v", err)
	}
	if !bytes.Equal(privKey, loadedPriv) {
		t.Error("loaded private key does not match saved private key")
	}

	// Load and compare public key
	loadedPub, err := LoadPublicKey(dir)
	if err != nil {
		t.Fatalf("LoadPublicKey() error: %v", err)
	}
	if loadedPub != string(pubKey) {
		t.Error("loaded public key does not match saved public key")
	}
}

func TestSaveKeyPair_FilePermissions(t *testing.T) {
	dir := t.TempDir()

	pubKey, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	if err := SaveKeyPair(dir, privKey, pubKey); err != nil {
		t.Fatalf("SaveKeyPair() error: %v", err)
	}

	privInfo, err := os.Stat(filepath.Join(dir, privateKeyFile))
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if perm := privInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("private key permissions: got %o, want 0600", perm)
	}

	pubInfo, err := os.Stat(filepath.Join(dir, publicKeyFile))
	if err != nil {
		t.Fatalf("stat public key: %v", err)
	}
	if perm := pubInfo.Mode().Perm(); perm != 0644 {
		t.Errorf("public key permissions: got %o, want 0644", perm)
	}
}

func TestSaveKeyPair_InvalidDir(t *testing.T) {
	err := SaveKeyPair("/nonexistent/path", []byte("priv"), []byte("pub"))
	if err == nil {
		t.Error("SaveKeyPair() expected error for invalid directory")
	}
}

func TestKeyPairExists(t *testing.T) {
	dir := t.TempDir()

	// Neither file exists
	if KeyPairExists(dir) {
		t.Error("KeyPairExists() = true for empty directory")
	}

	// Only private key exists
	if err := os.WriteFile(filepath.Join(dir, privateKeyFile), []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}
	if KeyPairExists(dir) {
		t.Error("KeyPairExists() = true with only private key")
	}

	// Both files exist
	if err := os.WriteFile(filepath.Join(dir, publicKeyFile), []byte("key"), 0644); err != nil {
		t.Fatal(err)
	}
	if !KeyPairExists(dir) {
		t.Error("KeyPairExists() = false with both keys present")
	}
}

func TestKeyPairExists_NonexistentDir(t *testing.T) {
	if KeyPairExists("/nonexistent/path") {
		t.Error("KeyPairExists() = true for nonexistent directory")
	}
}

func TestLoadPrivateKey_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadPrivateKey(dir)
	if err == nil {
		t.Error("LoadPrivateKey() expected error for missing file")
	}
}

func TestLoadPublicKey_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadPublicKey(dir)
	if err == nil {
		t.Error("LoadPublicKey() expected error for missing file")
	}
}

func TestParsePrivateKey_Valid(t *testing.T) {
	_, privKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	signer, err := ParsePrivateKey(privKey)
	if err != nil {
		t.Fatalf("ParsePrivateKey() error: %v", err)
	}
	if signer == nil {
		t.Fatal("ParsePrivateKey() returned nil signer")
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("key type: got %s, want ssh-ed25519", signer.PublicKey().Type())
	}
}

func TestParsePrivateKey_Invalid(t *testing.T) {
	_, err := ParsePrivateKey([]byte("not a valid key"))
	if err == nil {
		t.Error("ParsePrivateKey() expected error for invalid key data")
	}
}

func TestParsePrivateKey_RoundTrip(t *testing.T) {
	pubKeyBytes, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Save, load, then parse
	dir := t.TempDir()
	if err := SaveKeyPair(dir, privKeyPEM, pubKeyBytes); err != nil {
		t.Fatalf("SaveKeyPair() error: %v", err)
	}

	loadedPriv, err := LoadPrivateKey(dir)
	if err != nil {
		t.Fatalf("LoadPrivateKey() error: %v", err)
	}

	signer, err := ParsePrivateKey(loadedPriv)
	if err != nil {
		t.Fatalf("ParsePrivateKey() error: %v", err)
	}

	// Verify the parsed key matches the original public key
	loadedPub, err := LoadPublicKey(dir)
	if err != nil {
		t.Fatalf("LoadPublicKey() error: %v", err)
	}

	parsedPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(loadedPub))
	if err != nil {
		t.Fatalf("ParseAuthorizedKey() error: %v", err)
	}

	// Compare public key fingerprints
	signerFingerprint := ssh.FingerprintSHA256(signer.PublicKey())
	parsedFingerprint := ssh.FingerprintSHA256(parsedPub)
	if signerFingerprint != parsedFingerprint {
		t.Errorf("fingerprint mismatch: signer=%s, parsed=%s", signerFingerprint, parsedFingerprint)
	}
}

func TestEnsureKeyPair_FirstRun(t *testing.T) {
	dir := t.TempDir()

	// No keys exist yet
	if KeyPairExists(dir) {
		t.Fatal("expected no keys before first run")
	}

	signer, pubKey, err := EnsureKeyPair(dir)
	if err != nil {
		t.Fatalf("EnsureKeyPair() error: %v", err)
	}
	if signer == nil {
		t.Fatal("EnsureKeyPair() returned nil signer")
	}
	if pubKey == "" {
		t.Fatal("EnsureKeyPair() returned empty public key")
	}

	// Keys should now exist on disk
	if !KeyPairExists(dir) {
		t.Fatal("expected keys to exist after EnsureKeyPair")
	}

	// Signer should be ED25519
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Errorf("key type: got %s, want ssh-ed25519", signer.PublicKey().Type())
	}

	// Public key should be valid OpenSSH format
	_, _, _, _, err = ssh.ParseAuthorizedKey([]byte(pubKey))
	if err != nil {
		t.Fatalf("returned public key is not valid OpenSSH format: %v", err)
	}
}

func TestEnsureKeyPair_SecondRunPersistence(t *testing.T) {
	dir := t.TempDir()

	// First run: generate keys
	signer1, pubKey1, err := EnsureKeyPair(dir)
	if err != nil {
		t.Fatalf("first EnsureKeyPair() error: %v", err)
	}

	// Second run: should load same keys
	signer2, pubKey2, err := EnsureKeyPair(dir)
	if err != nil {
		t.Fatalf("second EnsureKeyPair() error: %v", err)
	}

	// Public keys should be identical
	if pubKey1 != pubKey2 {
		t.Error("public key changed between runs")
	}

	// Signer fingerprints should match
	fp1 := ssh.FingerprintSHA256(signer1.PublicKey())
	fp2 := ssh.FingerprintSHA256(signer2.PublicKey())
	if fp1 != fp2 {
		t.Errorf("signer fingerprint changed between runs: %s != %s", fp1, fp2)
	}
}

func TestEnsureKeyPair_FilePermissions(t *testing.T) {
	dir := t.TempDir()

	_, _, err := EnsureKeyPair(dir)
	if err != nil {
		t.Fatalf("EnsureKeyPair() error: %v", err)
	}

	privInfo, err := os.Stat(filepath.Join(dir, privateKeyFile))
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if perm := privInfo.Mode().Perm(); perm != 0600 {
		t.Errorf("private key permissions: got %o, want 0600", perm)
	}

	pubInfo, err := os.Stat(filepath.Join(dir, publicKeyFile))
	if err != nil {
		t.Fatalf("stat public key: %v", err)
	}
	if perm := pubInfo.Mode().Perm(); perm != 0644 {
		t.Errorf("public key permissions: got %o, want 0644", perm)
	}
}

func TestEnsureKeyPair_MissingDirectory(t *testing.T) {
	_, _, err := EnsureKeyPair("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("EnsureKeyPair() expected error for missing directory")
	}
}
