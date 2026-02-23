package sshkeys

import (
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

func TestGetPublicKeyFingerprint_Valid(t *testing.T) {
	pubKeyBytes, _, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	fp, err := GetPublicKeyFingerprint(pubKeyBytes)
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint() error: %v", err)
	}

	if len(fp) < 7 || fp[:7] != "SHA256:" {
		t.Errorf("GetPublicKeyFingerprint() = %q, want SHA256:... prefix", fp)
	}
}

func TestGetPublicKeyFingerprint_ConsistentAcrossCalls(t *testing.T) {
	pubKeyBytes, _, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	fp1, err := GetPublicKeyFingerprint(pubKeyBytes)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	fp2, err := GetPublicKeyFingerprint(pubKeyBytes)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if fp1 != fp2 {
		t.Errorf("fingerprint not consistent: %q != %q", fp1, fp2)
	}
}

func TestGetPublicKeyFingerprint_DifferentKeysProduceDifferentFingerprints(t *testing.T) {
	pubKey1, _, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair 1: %v", err)
	}

	pubKey2, _, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair 2: %v", err)
	}

	fp1, err := GetPublicKeyFingerprint(pubKey1)
	if err != nil {
		t.Fatalf("fingerprint 1 error: %v", err)
	}

	fp2, err := GetPublicKeyFingerprint(pubKey2)
	if err != nil {
		t.Fatalf("fingerprint 2 error: %v", err)
	}

	if fp1 == fp2 {
		t.Errorf("different keys produced same fingerprint: %q", fp1)
	}
}

func TestGetPublicKeyFingerprint_MatchesSSHManagerFingerprint(t *testing.T) {
	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}

	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	// Compute fingerprint via our standalone function
	fp, err := GetPublicKeyFingerprint(pubKeyBytes)
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint() error: %v", err)
	}

	// Compute fingerprint the same way SSHManager does (via ssh.FingerprintSHA256)
	expected := ssh.FingerprintSHA256(signer.PublicKey())

	if fp != expected {
		t.Errorf("fingerprint mismatch: got %q, want %q (from signer)", fp, expected)
	}
}

func TestGetPublicKeyFingerprint_InvalidKey(t *testing.T) {
	_, err := GetPublicKeyFingerprint([]byte("not a valid key"))
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
}

func TestGetPublicKeyFingerprint_EmptyKey(t *testing.T) {
	_, err := GetPublicKeyFingerprint([]byte{})
	if err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

func TestGetPublicKeyFingerprint_NilKey(t *testing.T) {
	_, err := GetPublicKeyFingerprint(nil)
	if err == nil {
		t.Fatal("expected error for nil key, got nil")
	}
}
