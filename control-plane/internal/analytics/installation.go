package analytics

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

const (
	settingInstallationID = "installation_id"
	settingConsent        = "analytics_consent"

	ConsentUnset  = "unset"
	ConsentOptIn  = "opt_in"
	ConsentOptOut = "opt_out"
)

// GetOrCreateInstallationID returns the installation ID from settings, creating
// one on first call. Mirrors the fernet-key bootstrap in internal/utils/crypto.go.
func GetOrCreateInstallationID() (string, error) {
	id, err := database.GetSetting(settingInstallationID)
	if err == nil && id != "" {
		return id, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate installation id: %w", err)
	}
	id = hex.EncodeToString(buf)
	if err := database.SetSetting(settingInstallationID, id); err != nil {
		return "", fmt.Errorf("save installation id: %w", err)
	}
	return id, nil
}

// GetConsent returns the current consent state, defaulting to ConsentUnset.
func GetConsent() string {
	v, err := database.GetSetting(settingConsent)
	if err != nil || v == "" {
		return ConsentUnset
	}
	return v
}
