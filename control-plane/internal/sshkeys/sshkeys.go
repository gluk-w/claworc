package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

const (
	privateKeyFile = "ssh_key"
	publicKeyFile  = "ssh_key.pub"
)

// GenerateKeyPair generates an ED25519 key pair and returns the PEM-encoded
// private key and OpenSSH-format public key.
func GenerateKeyPair() (publicKey, privateKeyPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal private key: %w", err)
	}

	privateKeyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privBytes,
	})

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, nil, fmt.Errorf("create ssh public key: %w", err)
	}
	publicKey = ssh.MarshalAuthorizedKey(sshPub)

	return publicKey, privateKeyPEM, nil
}

// SaveKeyPair writes the private and public key files to the given directory.
// The private key is written with mode 0600 and the public key with mode 0644.
func SaveKeyPair(dir string, privateKey, publicKey []byte) error {
	privPath := filepath.Join(dir, privateKeyFile)
	if err := os.WriteFile(privPath, privateKey, 0600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	pubPath := filepath.Join(dir, publicKeyFile)
	if err := os.WriteFile(pubPath, publicKey, 0644); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}

	log.Printf("SSH key pair saved to %s", dir)
	return nil
}

// LoadPrivateKey reads the private key file from the given directory.
func LoadPrivateKey(dir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, privateKeyFile))
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	return data, nil
}

// LoadPublicKey reads the public key file from the given directory and returns
// it as a string (OpenSSH authorized_keys format).
func LoadPublicKey(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, publicKeyFile))
	if err != nil {
		return "", fmt.Errorf("read public key: %w", err)
	}
	return string(data), nil
}

// KeyPairExists checks if both ssh_key and ssh_key.pub exist in the directory.
func KeyPairExists(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, privateKeyFile)); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, publicKeyFile)); err != nil {
		return false
	}
	return true
}

// ParsePrivateKey parses a PEM-encoded private key into an ssh.Signer for
// SSH authentication.
func ParsePrivateKey(privateKeyPEM []byte) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return signer, nil
}
