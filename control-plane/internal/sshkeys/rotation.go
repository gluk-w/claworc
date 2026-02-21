package sshkeys

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// RotationResult contains the outcome of a key rotation operation.
type RotationResult struct {
	NewFingerprint string
	RotatedAt      time.Time
}

// RotateKeyPair performs a zero-downtime SSH key rotation for an instance.
// It generates a new key pair, appends the new public key to the agent's
// authorized_keys, verifies connectivity with the new key, removes the old
// public key, and updates the on-disk private key.
//
// The caller is responsible for updating the database with the returned
// RotationResult and the new public key / private key path.
//
// Parameters:
//   - sshClient: an active SSH connection to the agent (using the current key)
//   - instanceName: the instance name (used for key file naming)
//   - oldPublicKey: the current public key in authorized_keys format
//   - host: SSH host for testing the new key
//   - port: SSH port for testing the new key
func RotateKeyPair(
	sshClient *ssh.Client,
	instanceName string,
	oldPublicKey string,
	host string,
	port int,
) (newPublicKey []byte, newPrivateKeyPath string, result *RotationResult, err error) {
	if sshClient == nil {
		return nil, "", nil, fmt.Errorf("rotate key: SSH client is nil")
	}
	if instanceName == "" {
		return nil, "", nil, fmt.Errorf("rotate key: instance name is empty")
	}

	log.Printf("[sshkeys] starting key rotation for instance %s", logutil.SanitizeForLog(instanceName))

	// Step 1: Generate new key pair
	newPubKey, newPrivKey, err := GenerateKeyPair()
	if err != nil {
		return nil, "", nil, fmt.Errorf("rotate key: generate new key pair: %w", err)
	}

	newAuthorizedKey, err := FormatPublicKeyForAuthorizedKeys(newPubKey)
	if err != nil {
		return nil, "", nil, fmt.Errorf("rotate key: format new public key: %w", err)
	}

	// Step 2: Save new private key to disk (with .new suffix temporarily)
	newKeyPath, err := SavePrivateKey(instanceName, newPrivKey)
	if err != nil {
		return nil, "", nil, fmt.Errorf("rotate key: save new private key: %w", err)
	}

	// Step 3: Append new public key to authorized_keys on the agent
	escapedKey := strings.TrimSpace(newAuthorizedKey)
	appendCmd := fmt.Sprintf("echo '%s' >> /root/.ssh/authorized_keys", escapedKey)
	if err := executeSSHCommand(sshClient, appendCmd); err != nil {
		// Clean up the new private key since rotation failed
		_ = DeletePrivateKey(newKeyPath)
		return nil, "", nil, fmt.Errorf("rotate key: append new public key to agent: %w", err)
	}

	log.Printf("[sshkeys] appended new public key to authorized_keys for %s", logutil.SanitizeForLog(instanceName))

	// Step 4: Test connection with new key
	if err := testSSHConnection(newKeyPath, host, port); err != nil {
		// Rollback: remove the new public key from authorized_keys
		_ = removePublicKeyFromAgent(sshClient, escapedKey)
		_ = DeletePrivateKey(newKeyPath)
		return nil, "", nil, fmt.Errorf("rotate key: test new key failed: %w", err)
	}

	log.Printf("[sshkeys] new key verified for %s", logutil.SanitizeForLog(instanceName))

	// Step 5: Remove old public key from authorized_keys using the new connection
	newClient, err := dialSSH(newKeyPath, host, port)
	if err != nil {
		// The new key was tested successfully, but we can't connect again.
		// Leave both keys in authorized_keys and proceed — the old key will remain
		// but the rotation is still functional.
		log.Printf("[sshkeys] WARNING: could not connect with new key to remove old key for %s: %v",
			logutil.SanitizeForLog(instanceName), err)
	} else {
		defer newClient.Close()
		oldKeyTrimmed := strings.TrimSpace(oldPublicKey)
		if oldKeyTrimmed != "" {
			if err := removePublicKeyFromAgent(newClient, oldKeyTrimmed); err != nil {
				log.Printf("[sshkeys] WARNING: failed to remove old public key for %s: %v",
					logutil.SanitizeForLog(instanceName), err)
			} else {
				log.Printf("[sshkeys] removed old public key from authorized_keys for %s",
					logutil.SanitizeForLog(instanceName))
			}
		}
	}

	// Step 6: Compute fingerprint for the result
	parsedPub, _, _, _, err := ssh.ParseAuthorizedKey(newPubKey)
	if err != nil {
		// Non-fatal — the rotation succeeded, just can't compute fingerprint
		log.Printf("[sshkeys] WARNING: could not compute fingerprint for new key: %v", err)
	}

	fingerprint := ""
	if parsedPub != nil {
		fingerprint = ssh.FingerprintSHA256(parsedPub)
	}

	now := time.Now()
	result = &RotationResult{
		NewFingerprint: fingerprint,
		RotatedAt:      now,
	}

	log.Printf("[sshkeys] key rotation completed for %s (fingerprint: %s)",
		logutil.SanitizeForLog(instanceName), fingerprint)

	return newPubKey, newKeyPath, result, nil
}

// executeSSHCommand runs a command on the remote agent via an existing SSH client.
func executeSSHCommand(client *ssh.Client, cmd string) error {
	if client == nil {
		return fmt.Errorf("execute command: SSH client is nil")
	}
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	defer session.Close()

	var stderr bytes.Buffer
	session.Stderr = &stderr

	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("run command %q: %w (stderr: %s)", cmd, err, stderr.String())
	}
	return nil
}

// testSSHConnection attempts to connect to the agent using the given private key
// and runs a simple echo command to verify the key works.
func testSSHConnection(privateKeyPath string, host string, port int) error {
	client, err := dialSSH(privateKeyPath, host, port)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("create test session: %w", err)
	}
	defer session.Close()

	var out bytes.Buffer
	session.Stdout = &out
	if err := session.Run("echo ping"); err != nil {
		return fmt.Errorf("test command failed: %w", err)
	}
	if strings.TrimSpace(out.String()) != "ping" {
		return fmt.Errorf("unexpected test output: %q", out.String())
	}
	return nil
}

// dialSSH creates a new SSH client connection using the given private key.
func dialSSH(privateKeyPath string, host string, port int) (*ssh.Client, error) {
	keyData, err := LoadPrivateKey(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load private key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return client, nil
}

// removePublicKeyFromAgent removes a specific public key line from the agent's
// authorized_keys file. It uses grep -v with a fixed-string match to filter out
// the key, then writes the result back.
func removePublicKeyFromAgent(client *ssh.Client, publicKeyLine string) error {
	// Use the key's base64 data portion for matching (more reliable than the full line)
	parts := strings.Fields(publicKeyLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid public key format: too few fields")
	}
	keyData := parts[1] // The base64-encoded key data

	// Filter out any line containing the old key data
	cmd := fmt.Sprintf(
		"grep -v -F '%s' /root/.ssh/authorized_keys > /root/.ssh/authorized_keys.tmp && mv /root/.ssh/authorized_keys.tmp /root/.ssh/authorized_keys",
		keyData,
	)
	return executeSSHCommand(client, cmd)
}
