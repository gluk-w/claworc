package sshkeys

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGetPublicKeyFingerprint_Valid(t *testing.T) {
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	fp, err := GetPublicKeyFingerprint(pubKey)
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint() error: %v", err)
	}

	// SHA256 fingerprints start with "SHA256:"
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Errorf("fingerprint should start with 'SHA256:', got %q", fp)
	}

	// Should have meaningful length beyond the prefix
	if len(fp) < 10 {
		t.Errorf("fingerprint too short: %q", fp)
	}
}

func TestGetPublicKeyFingerprint_Empty(t *testing.T) {
	_, err := GetPublicKeyFingerprint(nil)
	if err == nil {
		t.Fatal("expected error for nil key, got nil")
	}
	if !strings.Contains(err.Error(), "public key is empty") {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = GetPublicKeyFingerprint([]byte{})
	if err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

func TestGetPublicKeyFingerprint_InvalidKey(t *testing.T) {
	_, err := GetPublicKeyFingerprint([]byte("not-a-valid-key"))
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
	if !strings.Contains(err.Error(), "parse public key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetPublicKeyFingerprint_Deterministic(t *testing.T) {
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	fp1, err := GetPublicKeyFingerprint(pubKey)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	fp2, err := GetPublicKeyFingerprint(pubKey)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q != %q", fp1, fp2)
	}
}

func TestGetPublicKeyFingerprint_UniquePerKey(t *testing.T) {
	pub1, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("first GenerateKeyPair() error: %v", err)
	}

	pub2, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("second GenerateKeyPair() error: %v", err)
	}

	fp1, _ := GetPublicKeyFingerprint(pub1)
	fp2, _ := GetPublicKeyFingerprint(pub2)

	if fp1 == fp2 {
		t.Error("two different keys should produce different fingerprints")
	}
}

func TestGetPublicKeyAlgorithm_Valid(t *testing.T) {
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	algo, err := GetPublicKeyAlgorithm(pubKey)
	if err != nil {
		t.Fatalf("GetPublicKeyAlgorithm() error: %v", err)
	}

	if algo != "ssh-ed25519" {
		t.Errorf("expected 'ssh-ed25519', got %q", algo)
	}
}

func TestGetPublicKeyAlgorithm_Empty(t *testing.T) {
	_, err := GetPublicKeyAlgorithm(nil)
	if err == nil {
		t.Fatal("expected error for nil key, got nil")
	}
	if !strings.Contains(err.Error(), "public key is empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetPublicKeyAlgorithm_Invalid(t *testing.T) {
	_, err := GetPublicKeyAlgorithm([]byte("invalid"))
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
	if !strings.Contains(err.Error(), "parse public key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVerifyFingerprint_Match(t *testing.T) {
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	fp, err := GetPublicKeyFingerprint(pubKey)
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint() error: %v", err)
	}

	if err := VerifyFingerprint(pubKey, fp); err != nil {
		t.Errorf("VerifyFingerprint should pass for matching fingerprint: %v", err)
	}
}

func TestVerifyFingerprint_Mismatch(t *testing.T) {
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	err = VerifyFingerprint(pubKey, "SHA256:bogus-fingerprint")
	if err == nil {
		t.Fatal("expected error for mismatched fingerprint, got nil")
	}

	var mismatchErr *FingerprintMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("expected FingerprintMismatchError, got %T: %v", err, err)
	}

	if mismatchErr.Expected != "SHA256:bogus-fingerprint" {
		t.Errorf("expected 'SHA256:bogus-fingerprint', got %q", mismatchErr.Expected)
	}

	if !strings.HasPrefix(mismatchErr.Actual, "SHA256:") {
		t.Errorf("actual should start with 'SHA256:', got %q", mismatchErr.Actual)
	}
}

func TestVerifyFingerprint_EmptyExpected(t *testing.T) {
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Empty expected should be treated as TOFU (Trust On First Use) — no error
	if err := VerifyFingerprint(pubKey, ""); err != nil {
		t.Errorf("empty expected fingerprint should pass (TOFU): %v", err)
	}
}

func TestVerifyFingerprint_InvalidPublicKey(t *testing.T) {
	err := VerifyFingerprint([]byte("invalid-key"), "SHA256:something")
	if err == nil {
		t.Fatal("expected error for invalid public key, got nil")
	}
	if !strings.Contains(err.Error(), "verify fingerprint") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVerifyFingerprint_DifferentKeys(t *testing.T) {
	pub1, _, _ := GenerateKeyPair()
	pub2, _, _ := GenerateKeyPair()

	fp1, _ := GetPublicKeyFingerprint(pub1)

	// Verify pub2 against pub1's fingerprint should fail
	err := VerifyFingerprint(pub2, fp1)
	if err == nil {
		t.Fatal("expected mismatch error when verifying different key against stored fingerprint")
	}

	var mismatchErr *FingerprintMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("expected FingerprintMismatchError, got %T: %v", err, err)
	}
}

func TestFingerprintMismatchError_Message(t *testing.T) {
	err := &FingerprintMismatchError{
		Expected: "SHA256:expected123",
		Actual:   "SHA256:actual456",
	}

	msg := err.Error()
	if !strings.Contains(msg, "expected123") {
		t.Errorf("error message should contain expected fingerprint: %s", msg)
	}
	if !strings.Contains(msg, "actual456") {
		t.Errorf("error message should contain actual fingerprint: %s", msg)
	}
	if !strings.Contains(msg, "mismatch") {
		t.Errorf("error message should mention mismatch: %s", msg)
	}
}

func TestMakeHostKeyCallback_NoExpected(t *testing.T) {
	cb, actualFP := MakeHostKeyCallback("")
	if cb == nil {
		t.Fatal("callback should not be nil")
	}
	if actualFP == nil {
		t.Fatal("actualFP pointer should not be nil")
	}
	// Before any call, actualFP should be empty
	if *actualFP != "" {
		t.Errorf("expected empty actualFP before callback, got %q", *actualFP)
	}
}

func TestMakeHostKeyCallback_WithExpected_Match(t *testing.T) {
	// Generate a key to simulate a host key
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	fp, _ := GetPublicKeyFingerprint(pubKey)

	// Create callback expecting this fingerprint
	cb, actualFP := MakeHostKeyCallback(fp)

	// Parse the public key
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(pubKey)
	if err != nil {
		t.Fatalf("ssh.ParseAuthorizedKey() error: %v", err)
	}

	// Call the callback
	callErr := cb("test-host:22", nil, parsed)
	if callErr != nil {
		t.Errorf("callback should not error when fingerprints match: %v", callErr)
	}

	if *actualFP != fp {
		t.Errorf("actualFP should be %q, got %q", fp, *actualFP)
	}
}

func TestMakeHostKeyCallback_WithExpected_Mismatch(t *testing.T) {
	// Generate a key to simulate a host key
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Create callback with a bogus expected fingerprint
	cb, actualFP := MakeHostKeyCallback("SHA256:bogus-fingerprint")

	// Parse the public key
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(pubKey)
	if err != nil {
		t.Fatalf("ssh.ParseAuthorizedKey() error: %v", err)
	}

	// Call the callback — should NOT error (we just log warnings for host keys)
	callErr := cb("test-host:22", nil, parsed)
	if callErr != nil {
		t.Errorf("callback should not reject host key changes (just warn): %v", callErr)
	}

	// actualFP should contain the real fingerprint
	if !strings.HasPrefix(*actualFP, "SHA256:") {
		t.Errorf("actualFP should be a SHA256 fingerprint, got %q", *actualFP)
	}
}

func TestEndToEndFingerprintWorkflow(t *testing.T) {
	// Generate key pair
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Compute fingerprint (simulates what happens at key creation time)
	storedFP, err := GetPublicKeyFingerprint(pubKey)
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint() error: %v", err)
	}

	// Verify the fingerprint (simulates check before SSH connection)
	if err := VerifyFingerprint(pubKey, storedFP); err != nil {
		t.Fatalf("VerifyFingerprint should pass: %v", err)
	}

	// Simulate key rotation: generate new key
	newPubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() for new key: %v", err)
	}

	newFP, err := GetPublicKeyFingerprint(newPubKey)
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint() for new key: %v", err)
	}

	// Old fingerprint should NOT match new key
	err = VerifyFingerprint(newPubKey, storedFP)
	if err == nil {
		t.Fatal("old fingerprint should not match new key")
	}

	// New fingerprint should match new key
	if err := VerifyFingerprint(newPubKey, newFP); err != nil {
		t.Fatalf("new fingerprint should match new key: %v", err)
	}
}

func TestGetPublicKeyFingerprint_MatchesSSHLibrary(t *testing.T) {
	// Verify our function produces the same result as directly using the ssh library
	pubKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error: %v", err)
	}

	// Our function
	ourFP, err := GetPublicKeyFingerprint(pubKey)
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint() error: %v", err)
	}

	// Direct ssh library
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(pubKey)
	if err != nil {
		t.Fatalf("ssh.ParseAuthorizedKey() error: %v", err)
	}
	directFP := ssh.FingerprintSHA256(parsed)

	if ourFP != directFP {
		t.Errorf("our fingerprint %q doesn't match ssh library %q", ourFP, directFP)
	}
}
