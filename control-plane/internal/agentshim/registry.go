package agentshim

import (
	"encoding/json"
	"fmt"
)

// Agent type identifiers known to the control plane. "openclaw" matches
// openclawnative.Type and models.AgentTypeOpenClaw (kept as literals here to
// avoid an import cycle with the adapter package).
const (
	TypeOpenClaw = "openclaw"
	TypeHermes   = "hermes"
	TypeNanoClaw = "nanoclaw"
	TypeCustom   = "custom"
)

// Setting keys used for per-type default image resolution.
const (
	// SettingDefaultAgentImage is the legacy single-image setting; it remains
	// the default image source for the OpenClaw agent type.
	SettingDefaultAgentImage = "default_agent_image"
	// SettingDefaultAgentImages is a JSON map {agentType: image} holding the
	// default images of every non-OpenClaw agent type.
	SettingDefaultAgentImages = "default_agent_images"
)

// RegistryEntry describes one agent type the control plane can manage. The
// registry is static: it captures what is known about a type before any
// container exists (display name, conservative capabilities, log path).
// Live capability probing — once shim-capable images land — refines, never
// replaces, this data.
type RegistryEntry struct {
	// Type is the identifier stored in Instance.AgentType.
	Type string
	// DisplayName is the human-readable agent name shown in the UI.
	DisplayName string
	// HasControlUI reports whether the agent serves its own web control UI
	// that the control plane reverse-proxies (/openclaw/{id}/*).
	HasControlUI bool
	// StaticCapabilities are the capabilities assumed for instances of this
	// type without probing the container. OpenClaw's are exact (the adapter
	// is built in); other types are conservative.
	StaticCapabilities Capabilities
	// LogPath is the primary agent log file inside the container.
	LogPath string
}

// registryEntries is the ordered static registry. Order is the UI display
// order: the incumbent first, then alphabetical, custom last.
var registryEntries = []RegistryEntry{
	{
		Type:         TypeOpenClaw,
		DisplayName:  "OpenClaw",
		HasControlUI: true,
		StaticCapabilities: Capabilities{
			Chat:               true,
			ChatAbort:          true,
			SessionReset:       true,
			Config:             true,
			ConfigureLLM:       true,
			Restart:            true,
			ControlUI:          true,
			Skills:             true,
			LLMStyles:          []string{"openai"},
			SessionPersistence: "native",
		},
		LogPath: "/var/log/claworc/openclaw.log",
	},
	{
		Type:        TypeHermes,
		DisplayName: "Hermes",
		StaticCapabilities: Capabilities{
			Chat:         true,
			Config:       true,
			ConfigureLLM: true,
			Restart:      true,
		},
		LogPath: "/var/log/claworc/agent.log",
	},
	{
		Type:        TypeNanoClaw,
		DisplayName: "NanoClaw",
		StaticCapabilities: Capabilities{
			Chat:         true,
			ConfigureLLM: true,
			Restart:      true,
		},
		LogPath: "/var/log/claworc/agent.log",
	},
	{
		Type:        TypeCustom,
		DisplayName: "Custom",
		StaticCapabilities: Capabilities{
			Chat:         true,
			Config:       true,
			ConfigureLLM: true,
			Restart:      true,
		},
		LogPath: "/var/log/claworc/agent.log",
	},
}

// Types returns the ordered list of registered agent types.
func Types() []RegistryEntry {
	out := make([]RegistryEntry, len(registryEntries))
	copy(out, registryEntries)
	return out
}

// Get returns the registry entry for an agent type. The empty string resolves
// to OpenClaw, mirroring Instance.EffectiveAgentType.
func Get(agentType string) (RegistryEntry, bool) {
	if agentType == "" {
		agentType = TypeOpenClaw
	}
	for _, e := range registryEntries {
		if e.Type == agentType {
			return e, true
		}
	}
	return RegistryEntry{}, false
}

// Validate returns an error when agentType is not a registered agent type.
// The empty string is valid (it means OpenClaw).
func Validate(agentType string) error {
	if _, ok := Get(agentType); !ok {
		return fmt.Errorf("unknown agent type %q (valid: openclaw, hermes, nanoclaw, custom)", agentType)
	}
	return nil
}

// DefaultImage resolves the configured default container image for an agent
// type. OpenClaw reads the legacy default_agent_image setting; every other
// type reads its key out of the default_agent_images JSON map. getSetting is
// injected (normally database.GetSetting) so this stays testable without a
// DB. Returns "" when nothing is configured.
func DefaultImage(agentType string, getSetting func(key string) (string, error)) string {
	if agentType == "" || agentType == TypeOpenClaw {
		if v, err := getSetting(SettingDefaultAgentImage); err == nil {
			return v
		}
		return ""
	}
	raw, err := getSetting(SettingDefaultAgentImages)
	if err != nil || raw == "" {
		return ""
	}
	var images map[string]string
	if err := json.Unmarshal([]byte(raw), &images); err != nil {
		return ""
	}
	return images[agentType]
}
