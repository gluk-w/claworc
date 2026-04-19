package handlers

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// ReservedEnvVarNames are set by the control plane at container-create time and
// must not be shadowed by user-defined env vars. Every other OPENCLAW_* or
// CLAWORC_* name is allowed (users often need to configure OpenClaw itself or
// related tooling through them).
var ReservedEnvVarNames = []string{
	"OPENCLAW_GATEWAY_TOKEN",
	"CLAWORC_INSTANCE_ID",
	"OPENCLAW_INITIAL_MODELS",
	"OPENCLAW_INITIAL_PROVIDERS",
}

var envVarNameRegex = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// ValidateEnvVarName returns an error if name is not a valid POSIX-style env
// var name or if it collides with a reserved internal name.
func ValidateEnvVarName(name string) error {
	if !envVarNameRegex.MatchString(name) {
		return fmt.Errorf("invalid env var name %q: must match [A-Z_][A-Z0-9_]*", name)
	}
	for _, reserved := range ReservedEnvVarNames {
		if name == reserved {
			return fmt.Errorf("env var name %q is reserved for internal use", name)
		}
	}
	return nil
}

// encodeEncryptedEnvVars encrypts every value in the plaintext map and
// serializes the result as JSON suitable for database storage.
func encodeEncryptedEnvVars(plain map[string]string) (string, error) {
	if len(plain) == 0 {
		return "{}", nil
	}
	encrypted := make(map[string]string, len(plain))
	for k, v := range plain {
		enc, err := utils.Encrypt(v)
		if err != nil {
			return "", fmt.Errorf("encrypt %s: %w", k, err)
		}
		encrypted[k] = enc
	}
	b, err := json.Marshal(encrypted)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeEncryptedEnvVarsJSON decodes a JSON map {KEY: ciphertext} into a map
// of ciphertext values (no decryption).
func decodeEncryptedEnvVarsJSON(raw string) map[string]string {
	if raw == "" || raw == "{}" {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return map[string]string{}
	}
	return m
}

// decryptEnvVars decrypts each ciphertext value; any value that fails to
// decrypt is silently skipped so a single bad entry cannot break an instance.
func decryptEnvVars(encrypted map[string]string) map[string]string {
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


// LoadGlobalEnvVars reads default_env_vars from the settings table and returns
// the decrypted {KEY: value} map. Errors loading the setting are swallowed —
// env vars are optional enrichment, not critical infrastructure.
func LoadGlobalEnvVars() map[string]string {
	raw, err := database.GetSetting("default_env_vars")
	if err != nil {
		return map[string]string{}
	}
	return decryptEnvVars(decodeEncryptedEnvVarsJSON(raw))
}

// LoadInstanceEnvVars returns the decrypted per-instance env vars map.
func LoadInstanceEnvVars(inst database.Instance) map[string]string {
	return decryptEnvVars(decodeEncryptedEnvVarsJSON(inst.EnvVars))
}

// LoadGlobalEnvVarKeys returns the set of keys defined globally, sorted, with
// no decryption. Useful for callers that only need names (e.g. skill required-
// env-var checks).
func LoadGlobalEnvVarKeys() []string {
	raw, err := database.GetSetting("default_env_vars")
	if err != nil {
		return nil
	}
	m := decodeEncryptedEnvVarsJSON(raw)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MergeUserEnvVars applies global defaults first, then per-instance overrides,
// into the target map. Keys already present in target are overwritten — the
// caller is expected to re-apply reserved/system env vars afterwards so they
// always win.
func MergeUserEnvVars(target, global, instance map[string]string) {
	for k, v := range global {
		target[k] = v
	}
	for k, v := range instance {
		target[k] = v
	}
}

// ApplyEnvVarsDelta applies a set+unset delta to an existing JSON-encoded map
// of {KEY: ciphertext} and returns the new JSON, a `changed` flag (true iff
// the plaintext set differs), and any error.
//
// Change detection compares decrypted plaintexts — Fernet output is
// non-deterministic so a direct JSON string compare would always claim
// "changed". Callers can skip side effects (DB write, container restart) when
// changed=false.
//
// Name validation is performed up-front; invalid names fail before any write.
func ApplyEnvVarsDelta(existing string, set map[string]string, unset []string) (string, bool, error) {
	for name := range set {
		if err := ValidateEnvVarName(name); err != nil {
			return "", false, err
		}
	}
	for _, name := range unset {
		if err := ValidateEnvVarName(name); err != nil {
			return "", false, err
		}
	}

	existingPlain := decryptEnvVars(decodeEncryptedEnvVarsJSON(existing))
	newPlain := make(map[string]string, len(existingPlain)+len(set))
	for k, v := range existingPlain {
		newPlain[k] = v
	}
	for name, value := range set {
		newPlain[name] = value
	}
	for _, name := range unset {
		delete(newPlain, name)
	}

	if reflect.DeepEqual(existingPlain, newPlain) {
		return existing, false, nil
	}

	encoded, err := encodeEncryptedEnvVars(newPlain)
	if err != nil {
		return "", false, err
	}
	return encoded, true, nil
}

// UpsertEncryptedEnvVarsJSON is the legacy wrapper around ApplyEnvVarsDelta
// kept for callers that don't need the change signal (CreateInstance path
// where there is no prior state to compare against).
func UpsertEncryptedEnvVarsJSON(existing string, set map[string]string, unset []string) (string, error) {
	encoded, _, err := ApplyEnvVarsDelta(existing, set, unset)
	return encoded, err
}

// EnvVarsForResponse decodes the stored JSON, decrypts each value, and returns
// the plaintext KEY -> value map for inclusion in API responses. Admin-only
// endpoints surface these as-is; values are only ever encrypted at rest.
func EnvVarsForResponse(storedJSON string) map[string]string {
	return decryptEnvVars(decodeEncryptedEnvVarsJSON(storedJSON))
}
