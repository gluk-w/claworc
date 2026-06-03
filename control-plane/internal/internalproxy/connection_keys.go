// connection_keys.go manages the per-instance secret used to authenticate an
// OpenClaw instance to the /connections/ Composio broker. Connection secrets
// use the "claworc-cs-<random>" prefix and are injected into the container as
// the CLAWORC_CONNECTION_SECRET env var.

package internalproxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// generateConnectionSecret returns a unique secret with the "claworc-cs-" prefix.
func generateConnectionSecret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate connection secret: %v", err))
	}
	return "claworc-cs-" + hex.EncodeToString(b)
}

// hashConnectionSecret returns the hex SHA-256 of the plaintext secret. The hash
// is stored in an indexed column so the proxy can resolve an instance from the
// presented secret in O(1) without decrypting every row (Fernet ciphertext is
// non-deterministic and therefore not directly queryable).
func hashConnectionSecret(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// EnsureConnectionSecret returns the instance's plaintext connection secret,
// generating and persisting one (Fernet-encrypted, with its hash indexed) if the
// instance does not have one yet. Idempotent. The bool return reports whether a
// new secret was generated.
func EnsureConnectionSecret(instanceID uint) (plaintext string, generated bool, err error) {
	var inst database.Instance
	if dbErr := database.DB.First(&inst, instanceID).Error; dbErr != nil {
		return "", false, fmt.Errorf("load instance %d: %w", instanceID, dbErr)
	}
	if inst.ConnectionSecret != "" {
		plain, decErr := utils.Decrypt(inst.ConnectionSecret)
		if decErr == nil && plain != "" {
			return plain, false, nil
		}
		// Fall through and regenerate if the stored value is unreadable.
	}

	plaintext = generateConnectionSecret()
	enc, encErr := utils.Encrypt(plaintext)
	if encErr != nil {
		return "", false, fmt.Errorf("encrypt connection secret: %w", encErr)
	}
	if dbErr := database.DB.Model(&database.Instance{}).Where("id = ?", instanceID).Updates(map[string]any{
		"connection_secret":      enc,
		"connection_secret_hash": hashConnectionSecret(plaintext),
	}).Error; dbErr != nil {
		return "", false, fmt.Errorf("persist connection secret: %w", dbErr)
	}
	return plaintext, true, nil
}

// resolveInstanceBySecret looks up the instance that owns the given plaintext
// connection secret via its indexed hash.
func resolveInstanceBySecret(plaintext string) (*database.Instance, error) {
	if plaintext == "" {
		return nil, fmt.Errorf("empty connection secret")
	}
	var inst database.Instance
	if err := database.DB.Where("connection_secret_hash = ?", hashConnectionSecret(plaintext)).First(&inst).Error; err != nil {
		return nil, fmt.Errorf("unknown connection secret")
	}
	return &inst, nil
}
