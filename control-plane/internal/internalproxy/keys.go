// keys.go manages per-instance per-provider virtual keys for the internal
// proxy's LLM route. Virtual keys use the "claworc-vk-<random>" prefix.

package internalproxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// generateVirtualKey generates a unique LLM proxy virtual key with "claworc-vk-" prefix.
func generateVirtualKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate virtual key: %v", err))
	}
	return "claworc-vk-" + hex.EncodeToString(b)
}

// EnsureKeysForInstance creates virtual keys for each enabled provider (if not already present)
// and removes keys for providers that are no longer enabled.
func EnsureKeysForInstance(instanceID uint, enabledProviderIDs []uint) error {
	// Create keys for newly enabled providers
	for _, providerID := range enabledProviderIDs {
		var existing database.LLMProxyKey
		err := database.DB.Where("instance_id = ? AND provider_id = ?", instanceID, providerID).First(&existing).Error
		if err == nil {
			continue // already has a key
		}
		key := database.LLMProxyKey{
			InstanceID: instanceID,
			ProviderID: providerID,
			VirtualKey: generateVirtualKey(),
		}
		if err := database.DB.Create(&key).Error; err != nil {
			return fmt.Errorf("create virtual key for instance %d, provider %d: %w", instanceID, providerID, err)
		}
		log.Print("LLM proxy: created virtual key")
	}

	// Remove keys for disabled providers
	if len(enabledProviderIDs) == 0 {
		database.DB.Where("instance_id = ?", instanceID).Delete(&database.LLMProxyKey{})
	} else {
		database.DB.Where("instance_id = ? AND provider_id NOT IN ?", instanceID, enabledProviderIDs).Delete(&database.LLMProxyKey{})
	}

	return nil
}

// GetInstanceVirtualKeys returns a map of providerID → virtualKey for the given instance.
func GetInstanceVirtualKeys(instanceID uint) map[uint]string {
	var keys []database.LLMProxyKey
	database.DB.Where("instance_id = ?", instanceID).Find(&keys)
	result := make(map[uint]string, len(keys))
	for _, k := range keys {
		result[k.ProviderID] = k.VirtualKey
	}
	return result
}
