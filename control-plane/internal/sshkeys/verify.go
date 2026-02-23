package sshkeys

import (
	"fmt"

	"golang.org/x/crypto/ssh"
)

// GetPublicKeyFingerprint calculates the SHA256 fingerprint of an SSH public key.
// The publicKey parameter should be in OpenSSH authorized_keys format
// (e.g., "ssh-ed25519 AAAA... comment\n").
// Returns the fingerprint in standard format (SHA256:xxx).
func GetPublicKeyFingerprint(publicKey []byte) (string, error) {
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("parse public key: %w", err)
	}
	return ssh.FingerprintSHA256(pubKey), nil
}
