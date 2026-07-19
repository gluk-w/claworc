// hostkey.go manages the gateway's own SSH host key, distinct from the
// control plane's client key pair in sshproxy (ssh_key/ssh_key.pub).

package sshgateway

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

const (
	hostKeyFile    = "ssh_gateway_host_key"
	hostKeyPubFile = "ssh_gateway_host_key.pub"
)

// EnsureHostKey loads the gateway's ED25519 host key from dir, generating
// and persisting one on first run.
func EnsureHostKey(dir string) (ssh.Signer, error) {
	privPath := filepath.Join(dir, hostKeyFile)

	if _, err := os.Stat(privPath); err != nil {
		pubKey, privKey, err := sshproxy.GenerateKeyPair()
		if err != nil {
			return nil, fmt.Errorf("generate gateway host key: %w", err)
		}
		if err := os.WriteFile(privPath, privKey, 0600); err != nil {
			return nil, fmt.Errorf("write gateway host key: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, hostKeyPubFile), pubKey, 0644); err != nil {
			return nil, fmt.Errorf("write gateway host public key: %w", err)
		}
	}

	pemBytes, err := os.ReadFile(privPath)
	if err != nil {
		return nil, fmt.Errorf("read gateway host key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse gateway host key: %w", err)
	}
	return signer, nil
}
