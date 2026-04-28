// Package envvars contains shared helpers for loading and merging the
// per-instance and global user-defined environment variables. The data is
// stored encrypted at rest (Fernet) under a JSON map; this package handles
// decoding and decryption so callers outside the handlers package
// (browserprov, etc.) can reuse the logic without importing handlers.
package envvars

import (
	"encoding/json"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// decode parses a {KEY: ciphertext} JSON map without decrypting.
func decode(raw string) map[string]string {
	if raw == "" || raw == "{}" {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return map[string]string{}
	}
	return m
}

// decrypt converts a ciphertext map into a plaintext map. Entries that fail
// to decrypt are silently skipped so one bad row can't break an instance.
func decrypt(encrypted map[string]string) map[string]string {
	if len(encrypted) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(encrypted))
	for k, v := range encrypted {
		plain, err := utils.Decrypt(v)
		if err != nil {
			continue
		}
		out[k] = plain
	}
	return out
}

// LoadGlobal reads default_env_vars from the settings table and returns the
// decrypted {KEY: value} map.
func LoadGlobal() map[string]string {
	raw, err := database.GetSetting("default_env_vars")
	if err != nil {
		return map[string]string{}
	}
	return decrypt(decode(raw))
}

// LoadInstance returns the decrypted per-instance env vars map.
func LoadInstance(inst database.Instance) map[string]string {
	return decrypt(decode(inst.EnvVars))
}

// Merge returns a new map containing global defaults overridden by per-instance
// entries. Reserved/system env vars must be applied by the caller afterwards.
func Merge(global, instance map[string]string) map[string]string {
	out := make(map[string]string, len(global)+len(instance))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range instance {
		out[k] = v
	}
	return out
}
