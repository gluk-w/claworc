// keys.go manages per-instance per-provider gateway auth keys.
// Gateway keys use the "claworc-vk-<random>" prefix.

package llmgateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// generateGatewayKey generates a unique gateway auth key with "claworc-vk-" prefix.
func generateGatewayKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate gateway key: %v", err))
	}
	return "claworc-vk-" + hex.EncodeToString(b)
}

// EnsureKeysForInstance creates gateway keys for each enabled provider (if not already present)
// and removes keys for providers that are no longer enabled.
func EnsureKeysForInstance(instanceID uint, enabledProviderIDs []uint) error {
	// Create keys for newly enabled providers
	for _, providerID := range enabledProviderIDs {
		var existing database.LLMGatewayKey
		err := database.DB.Where("instance_id = ? AND provider_id = ?", instanceID, providerID).First(&existing).Error
		if err == nil {
			continue // already has a key
		}
		key := database.LLMGatewayKey{
			InstanceID: instanceID,
			ProviderID: providerID,
			GatewayKey: generateGatewayKey(),
		}
		if err := database.DB.Create(&key).Error; err != nil {
			return fmt.Errorf("create gateway key for instance %d, provider %d: %w", instanceID, providerID, err)
		}
		log.Printf("LLM gateway: created key for instance %d, provider %d", uint(instanceID), uint(providerID))
	}

	// Remove keys for disabled providers
	if len(enabledProviderIDs) == 0 {
		database.DB.Where("instance_id = ?", instanceID).Delete(&database.LLMGatewayKey{})
	} else {
		database.DB.Where("instance_id = ? AND provider_id NOT IN ?", instanceID, enabledProviderIDs).Delete(&database.LLMGatewayKey{})
	}

	return nil
}

// GetInstanceGatewayKeys returns a map of providerID → gatewayKey for the given instance.
func GetInstanceGatewayKeys(instanceID uint) map[uint]string {
	var keys []database.LLMGatewayKey
	database.DB.Where("instance_id = ?", instanceID).Find(&keys)
	result := make(map[uint]string, len(keys))
	for _, k := range keys {
		result[k.ProviderID] = k.GatewayKey
	}
	return result
}
