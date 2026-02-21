package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// DefaultKeyDir is the base directory for storing SSH private keys.
const DefaultKeyDir = "/app/data/ssh-keys"

// GenerateKeyPair generates an ED25519 SSH key pair.
// Returns the public key in SSH wire format and the private key in PEM format.
func GenerateKeyPair() (publicKey, privateKey []byte, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return nil, nil, fmt.Errorf("convert public key to ssh format: %w", err)
	}
	publicKey = ssh.MarshalAuthorizedKey(sshPubKey)

	pemBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal private key to PEM: %w", err)
	}
	privateKey = pem.EncodeToMemory(pemBlock)

	log.Printf("[sshkeys] generated new ED25519 key pair")
	return publicKey, privateKey, nil
}

// SavePrivateKey writes a private key to disk at DefaultKeyDir/{instanceName}.key
// with 0600 permissions. Creates the directory if it doesn't exist.
func SavePrivateKey(instanceName string, privateKey []byte) (keyPath string, err error) {
	if instanceName == "" {
		return "", fmt.Errorf("save private key: instance name is empty")
	}

	keyDir := DefaultKeyDir
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return "", fmt.Errorf("create key directory %s: %w", keyDir, err)
	}

	keyPath = filepath.Join(keyDir, instanceName+".key")
	if err := os.WriteFile(keyPath, privateKey, 0600); err != nil {
		return "", fmt.Errorf("write private key to %s: %w", logutil.SanitizeForLog(keyPath), err)
	}

	log.Printf("[sshkeys] saved private key for instance %s", logutil.SanitizeForLog(instanceName))
	return keyPath, nil
}

// SavePrivateKeyToDir writes a private key to disk at the specified directory
// with 0600 permissions. Creates the directory if it doesn't exist.
func SavePrivateKeyToDir(keyDir, instanceName string, privateKey []byte) (keyPath string, err error) {
	if instanceName == "" {
		return "", fmt.Errorf("save private key: instance name is empty")
	}

	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return "", fmt.Errorf("create key directory %s: %w", keyDir, err)
	}

	keyPath = filepath.Join(keyDir, instanceName+".key")
	if err := os.WriteFile(keyPath, privateKey, 0600); err != nil {
		return "", fmt.Errorf("write private key to %s: %w", logutil.SanitizeForLog(keyPath), err)
	}

	log.Printf("[sshkeys] saved private key for instance %s", logutil.SanitizeForLog(instanceName))
	return keyPath, nil
}

// LoadPrivateKey reads a private key from disk.
func LoadPrivateKey(keyPath string) ([]byte, error) {
	if keyPath == "" {
		return nil, fmt.Errorf("load private key: key path is empty")
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key from %s: %w", logutil.SanitizeForLog(keyPath), err)
	}

	// Validate it's a valid PEM block
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("load private key: file %s does not contain a valid PEM block", logutil.SanitizeForLog(keyPath))
	}

	return data, nil
}

// FormatPublicKeyForAuthorizedKeys converts an SSH public key (in authorized_keys
// wire format) to a string suitable for writing to an authorized_keys file.
// The input should be the output of GenerateKeyPair's publicKey return value.
func FormatPublicKeyForAuthorizedKeys(publicKey []byte) (string, error) {
	if len(publicKey) == 0 {
		return "", fmt.Errorf("format public key: public key is empty")
	}

	// Parse to validate the key
	_, _, _, _, err := ssh.ParseAuthorizedKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("format public key: invalid SSH public key: %w", err)
	}

	// The key from MarshalAuthorizedKey already includes a trailing newline;
	// trim it for clean concatenation then re-add it.
	result := string(publicKey)
	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}

	return result + "\n", nil
}

// DeletePrivateKey removes a private key file from disk.
func DeletePrivateKey(keyPath string) error {
	if keyPath == "" {
		return nil
	}

	if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete private key %s: %w", logutil.SanitizeForLog(keyPath), err)
	}

	log.Printf("[sshkeys] deleted private key at %s", logutil.SanitizeForLog(keyPath))
	return nil
}
