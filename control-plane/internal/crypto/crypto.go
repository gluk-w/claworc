package crypto

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/fernet/fernet-go"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

func getKey() (*fernet.Key, error) {
	keyStr, err := database.GetSetting("fernet_key")
	if err != nil {
		// Generate new key
		var k fernet.Key
		k.Generate()
		keyStr = k.Encode()
		if err := database.SetSetting("fernet_key", keyStr); err != nil {
			return nil, fmt.Errorf("save fernet key: %w", err)
		}
		return &k, nil
	}

	key, err := fernet.DecodeKey(keyStr)
	if err != nil {
		return nil, fmt.Errorf("decode fernet key: %w", err)
	}
	return key, nil
}

func Encrypt(plaintext string) (string, error) {
	key, err := getKey()
	if err != nil {
		return "", err
	}
	tok, err := fernet.EncryptAndSign([]byte(plaintext), key)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return string(tok), nil
}

func Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	key, err := getKey()
	if err != nil {
		return "", err
	}
	msg := fernet.VerifyAndDecrypt([]byte(ciphertext), 0*time.Second, []*fernet.Key{key})
	if msg == nil {
		// Try as base64 — maybe it was stored raw
		decoded, err := base64.URLEncoding.DecodeString(ciphertext)
		if err != nil {
			return "", fmt.Errorf("decrypt: invalid token")
		}
		_ = decoded
		return "", fmt.Errorf("decrypt: invalid token")
	}
	return string(msg), nil
}

func Mask(value string) string {
	if value == "" {
		return ""
	}
	if len(value) > 4 {
		return "****" + value[len(value)-4:]
	}
	return "****"
}
