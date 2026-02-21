package sshkeys

import (
	"fmt"
	"log"
	"net"

	"golang.org/x/crypto/ssh"
)

// FingerprintMismatchError is returned when a key fingerprint does not match the
// expected value. This may indicate key tampering or a MITM attack.
type FingerprintMismatchError struct {
	Expected string
	Actual   string
}

func (e *FingerprintMismatchError) Error() string {
	return fmt.Sprintf("SSH key fingerprint mismatch: expected %s, got %s (possible key tampering or MITM attack)", e.Expected, e.Actual)
}

// GetPublicKeyFingerprint calculates the SHA256 fingerprint of an SSH public key.
// The publicKey should be in SSH authorized_keys format (e.g. "ssh-ed25519 AAAA...").
// Returns the fingerprint in standard format (SHA256:xxx).
func GetPublicKeyFingerprint(publicKey []byte) (string, error) {
	if len(publicKey) == 0 {
		return "", fmt.Errorf("get fingerprint: public key is empty")
	}

	parsed, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("get fingerprint: parse public key: %w", err)
	}

	return ssh.FingerprintSHA256(parsed), nil
}

// GetPublicKeyAlgorithm returns the algorithm type (e.g. "ssh-ed25519") of an
// SSH public key in authorized_keys format.
func GetPublicKeyAlgorithm(publicKey []byte) (string, error) {
	if len(publicKey) == 0 {
		return "", fmt.Errorf("get algorithm: public key is empty")
	}

	parsed, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("get algorithm: parse public key: %w", err)
	}

	return parsed.Type(), nil
}

// VerifyFingerprint checks that the given public key matches the expected
// fingerprint. Returns nil if the fingerprint matches or if expectedFingerprint
// is empty (first-use scenario). Returns a *FingerprintMismatchError if the
// fingerprints differ.
func VerifyFingerprint(publicKey []byte, expectedFingerprint string) error {
	if expectedFingerprint == "" {
		return nil // No fingerprint stored yet; skip verification (TOFU)
	}

	actual, err := GetPublicKeyFingerprint(publicKey)
	if err != nil {
		return fmt.Errorf("verify fingerprint: %w", err)
	}

	if actual != expectedFingerprint {
		return &FingerprintMismatchError{
			Expected: expectedFingerprint,
			Actual:   actual,
		}
	}

	return nil
}

// MakeHostKeyCallback creates an ssh.HostKeyCallback that records the remote
// host's public key fingerprint. If expectedFingerprint is non-empty, the
// callback logs a warning when the actual host key fingerprint differs (but
// does NOT reject the connection, since containerized agents regenerate host
// keys on restart). The actual fingerprint is written to *actualFingerprint
// after the callback executes, allowing callers to store it for future use.
func MakeHostKeyCallback(expectedFingerprint string) (ssh.HostKeyCallback, *string) {
	var actual string
	cb := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		actual = ssh.FingerprintSHA256(key)
		if expectedFingerprint != "" && expectedFingerprint != actual {
			log.Printf("[sshkeys] WARNING: host key fingerprint changed for %s — expected %s, got %s (may indicate pod restart or MITM)",
				hostname, expectedFingerprint, actual)
		}
		return nil
	}
	return cb, &actual
}
