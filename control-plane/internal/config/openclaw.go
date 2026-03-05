package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/logutil"
)

// InstanceOps defines the generic primitives needed to configure an instance.
// Both DockerOrchestrator and KubernetesOrchestrator satisfy this interface
// via Go's structural typing.
type InstanceOps interface {
	ExecInInstance(ctx context.Context, name string, cmd []string) (string, string, int, error)
	GetInstanceStatus(ctx context.Context, name string) (string, error)
}

// GatewayProvider holds the virtual auth key and bare model IDs for a gateway provider.
type GatewayProvider struct {
	Key    string
	Models []string // bare model IDs, without provider prefix
}

const pathClaworcKeys = "/etc/default/claworc-keys"

// ConfigureInstance writes API keys as environment variables and sets the
// model configuration on a running instance.
//
// API keys are written to /etc/default/claworc-keys as KEY=VALUE lines,
// which the gateway service picks up via EnvironmentFile=.
//
// Models are set via `openclaw config set agents.defaults.model ... --json`.
//
// gatewayProviders (optional) maps provider key → gateway auth key for configuring
// models.providers in OpenClaw to route through the internal LLM gateway.
// gatewayPort is the port the LLM gateway listens on (typically 40001).
func ConfigureInstance(ctx context.Context, ops InstanceOps, name string, models []string, apiKeys map[string]string, gatewayProviders map[string]GatewayProvider, gatewayPort int) {
	if len(models) == 0 && len(apiKeys) == 0 && len(gatewayProviders) == 0 {
		return
	}

	// Wait for instance to become running
	if !waitForRunning(ctx, ops, name, 120*time.Second) {
		log.Printf("Timed out waiting for %s to start; models/keys not configured", logutil.SanitizeForLog(name))
		return
	}

	// Write API keys as environment variables
	if len(apiKeys) > 0 {
		var lines []string
		for k, v := range apiKeys {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
		}
		data := strings.Join(lines, "\n") + "\n"
		b64 := base64.StdEncoding.EncodeToString([]byte(data))
		cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > %s", b64, pathClaworcKeys)}
		_, stderr, code, err := ops.ExecInInstance(ctx, name, cmd)
		if err != nil {
			log.Printf("Error writing API keys for %s: %v", logutil.SanitizeForLog(name), err)
			return
		}
		if code != 0 {
			log.Printf("Failed to write API keys for %s: %s", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr))
			return
		}
	}

	// Set model config via openclaw config set
	if len(models) > 0 {
		modelConfig := map[string]interface{}{
			"primary": models[0],
		}
		if len(models) > 1 {
			modelConfig["fallbacks"] = models[1:]
		} else {
			modelConfig["fallbacks"] = []string{}
		}
		modelJSON, err := json.Marshal(modelConfig)
		if err != nil {
			log.Printf("Error marshaling model config for %s: %v", logutil.SanitizeForLog(name), err)
			return
		}
		// Use base64 encoding to safely pass JSON through shell
		b64 := base64.StdEncoding.EncodeToString(modelJSON)
		cmd := []string{"su", "-", "claworc", "-c",
			fmt.Sprintf("openclaw config set agents.defaults.model \"$(echo '%s' | base64 -d)\" --json", b64)}
		_, stderr, code, err := ops.ExecInInstance(ctx, name, cmd)
		if err != nil {
			log.Printf("Error setting model config for %s: %v", logutil.SanitizeForLog(name), err)
			return
		}
		if code != 0 {
			log.Printf("Failed to set model config for %s: %s", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr))
			return
		}
	}

	// Set gateway providers config (models.providers) if any are enabled
	if len(gatewayProviders) > 0 && gatewayPort > 0 {
		type modelEntry struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		type providerCfg struct {
			BaseURL string       `json:"baseUrl"`
			API     string       `json:"api"`
			APIKey  string       `json:"apiKey"`
			Models  []modelEntry `json:"models,omitempty"`
		}
		providers := make(map[string]providerCfg, len(gatewayProviders))
		for providerKey, gp := range gatewayProviders {
			entries := make([]modelEntry, 0, len(gp.Models))
			for _, m := range gp.Models {
				entries = append(entries, modelEntry{ID: m, Name: m})
			}
			providers[providerKey] = providerCfg{
				BaseURL: fmt.Sprintf("http://127.0.0.1:%d", gatewayPort),
				API:     "openai-completions",
				APIKey:  gp.Key,
				Models:  entries,
			}
		}
		providersJSON, err := json.Marshal(providers)
		if err != nil {
			log.Printf("Error marshaling gateway providers for %s: %v", logutil.SanitizeForLog(name), err)
		} else {
			b64 := base64.StdEncoding.EncodeToString(providersJSON)
			cmd := []string{"su", "-", "claworc", "-c",
				fmt.Sprintf(`openclaw config set models.providers "$(echo '%s' | base64 -d)" --json`, b64)}
			_, stderr, code, err := ops.ExecInInstance(ctx, name, cmd)
			if err != nil {
				log.Printf("Error setting gateway providers for %s: %v", logutil.SanitizeForLog(name), err)
			} else if code != 0 {
				log.Printf("Failed to set gateway providers for %s: %s", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr))
			}
		}
	}

	// Restart gateway so it picks up new env vars and config
	cmd := []string{"su", "-", "claworc", "-c", "openclaw gateway stop"}
	_, stderr, code, err := ops.ExecInInstance(ctx, name, cmd)
	if err != nil {
		log.Printf("Error restarting gateway for %s: %v", logutil.SanitizeForLog(name), err)
		return
	}
	if code != 0 {
		log.Printf("Failed to restart gateway for %s: %s", logutil.SanitizeForLog(name), logutil.SanitizeForLog(stderr))
		return
	}
	log.Printf("Models and API keys configured for %s", logutil.SanitizeForLog(name))
}

func waitForRunning(ctx context.Context, ops InstanceOps, name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := ops.GetInstanceStatus(ctx, name)
		if err == nil && status == "running" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
	return false
}
