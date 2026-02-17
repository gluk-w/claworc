package handlers

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// ChannelInfo describes a single configured communication channel for an instance.
type ChannelInfo struct {
	Type     string           `json:"type"`     // e.g. "slack", "discord", "whatsapp", ...
	Accounts []ChannelAccount `json:"accounts"` // multi-account details
}

// ChannelAccount describes a single account within a channel.
type ChannelAccount struct {
	Name    string   `json:"name"`              // display name (from config) or account key
	Enabled *bool    `json:"enabled,omitempty"` // nil = true by default
	Groups  []string `json:"groups,omitempty"`  // group/channel/guild names
}

// channelsCache is an in-memory cache for channel info per instance.
var channelsCache = struct {
	mu      sync.RWMutex
	entries map[string]channelsCacheEntry // keyed by instance name
}{
	entries: make(map[string]channelsCacheEntry),
}

type channelsCacheEntry struct {
	channels  []ChannelInfo
	fetchedAt time.Time
}

const channelsCacheTTL = 1 * time.Minute

// Known channel types in the config
var knownChannelTypes = []string{
	"whatsapp", "telegram", "discord", "googlechat",
	"slack", "signal", "imessage", "msteams",
}

// getChannelsForInstance returns channel info for an instance, using a 1-minute cache.
func getChannelsForInstance(ctx context.Context, instanceName string, status string) []ChannelInfo {
	// Only fetch for running instances
	if status != "running" {
		return nil
	}

	// Check cache
	channelsCache.mu.RLock()
	if entry, ok := channelsCache.entries[instanceName]; ok {
		if time.Since(entry.fetchedAt) < channelsCacheTTL {
			channelsCache.mu.RUnlock()
			return entry.channels
		}
	}
	channelsCache.mu.RUnlock()

	// Fetch from instance
	orch := orchestrator.Get()
	if orch == nil {
		return nil
	}

	channels := fetchChannelsFromConfig(ctx, orch, instanceName)

	// Update cache
	channelsCache.mu.Lock()
	channelsCache.entries[instanceName] = channelsCacheEntry{
		channels:  channels,
		fetchedAt: time.Now(),
	}
	channelsCache.mu.Unlock()

	return channels
}

// fetchChannelsFromConfig reads /home/claworc/.openclaw/openclaw.json from the instance
// and parses the channels section.
func fetchChannelsFromConfig(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName string) []ChannelInfo {
	data, _, _, err := orch.ExecInInstance(ctx, instanceName, []string{"cat", orchestrator.PathOpenClawConfig})
	if err != nil {
		log.Printf("Failed to read openclaw config for %s: %v", instanceName, err)
		return nil
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &config); err != nil {
		log.Printf("Failed to parse openclaw config for %s: %v", instanceName, err)
		return nil
	}

	channelsRaw, ok := config["channels"]
	if !ok {
		return nil
	}

	var channelsMap map[string]json.RawMessage
	if err := json.Unmarshal(channelsRaw, &channelsMap); err != nil {
		log.Printf("Failed to parse channels section for %s: %v", instanceName, err)
		return nil
	}

	var result []ChannelInfo
	for _, channelType := range knownChannelTypes {
		channelRaw, ok := channelsMap[channelType]
		if !ok {
			continue
		}

		// Skip "defaults" key
		if channelType == "defaults" {
			continue
		}

		ci := parseChannelConfig(channelType, channelRaw)
		if ci != nil {
			result = append(result, *ci)
		}
	}

	return result
}

// parseChannelConfig extracts channel info from a raw JSON channel config.
// All channel types follow a similar pattern: they can have top-level fields
// and an optional "accounts" map for multi-account.
func parseChannelConfig(channelType string, raw json.RawMessage) *ChannelInfo {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}

	ci := &ChannelInfo{Type: channelType}

	// Check for multi-account configuration
	accountsRaw, hasAccounts := obj["accounts"]
	if hasAccounts {
		var accounts map[string]json.RawMessage
		if err := json.Unmarshal(accountsRaw, &accounts); err == nil {
			for key, accRaw := range accounts {
				acc := parseAccountEntry(key, accRaw, channelType)
				ci.Accounts = append(ci.Accounts, acc)
			}
		}
	}

	// If no multi-account, treat the whole thing as a single default account
	if len(ci.Accounts) == 0 {
		acc := parseAccountEntry("default", raw, channelType)
		ci.Accounts = append(ci.Accounts, acc)
	}

	return ci
}

// parseAccountEntry extracts account-level details (name, enabled, groups).
func parseAccountEntry(key string, raw json.RawMessage, channelType string) ChannelAccount {
	var obj map[string]json.RawMessage
	json.Unmarshal(raw, &obj)

	acc := ChannelAccount{Name: key}

	// Extract "name" field if present
	if nameRaw, ok := obj["name"]; ok {
		var name string
		if json.Unmarshal(nameRaw, &name) == nil && name != "" {
			acc.Name = name
		}
	}

	// Extract "enabled" field
	if enabledRaw, ok := obj["enabled"]; ok {
		var enabled bool
		if json.Unmarshal(enabledRaw, &enabled) == nil {
			acc.Enabled = &enabled
		}
	}

	// Extract groups/channels/guilds/teams depending on channel type
	acc.Groups = extractGroupNames(obj, channelType)

	return acc
}

// extractGroupNames pulls group/channel/guild/team names from the config.
func extractGroupNames(obj map[string]json.RawMessage, channelType string) []string {
	// Different channel types use different keys for group config
	groupKeys := []string{"groups", "channels", "guilds", "teams"}

	var names []string
	for _, gk := range groupKeys {
		raw, ok := obj[gk]
		if !ok {
			continue
		}
		var groupMap map[string]json.RawMessage
		if json.Unmarshal(raw, &groupMap) == nil {
			for k := range groupMap {
				names = append(names, k)
			}
		}
	}
	return names
}
