package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// InstanceOps defines the generic primitives needed to configure an instance.
// Both DockerOrchestrator and KubernetesOrchestrator satisfy this interface
// via Go's structural typing.
type InstanceOps interface {
	ExecInInstance(ctx context.Context, name string, cmd []string) (string, string, int, error)
	WriteFile(ctx context.Context, name string, path string, data []byte) error
	GetInstanceStatus(ctx context.Context, name string) (string, error)
}

const pathClaworcKeys = "/etc/default/claworc-keys"

// ConfigureInstance writes API keys as environment variables and sets the
// model configuration on a running instance.
//
// When proxy mode is enabled, writes *_BASE_URL pointing to the proxy and
// *_API_KEY set to the proxy token instead of real keys. Real keys are
// synced to the proxy separately.
//
// API keys are written to /etc/default/claworc-keys as KEY=VALUE lines,
// which the gateway service picks up via EnvironmentFile=.
//
// Models are set via `openclaw config set agents.defaults.model ... --json`.
func ConfigureInstance(ctx context.Context, ops InstanceOps, name string, models []string, apiKeys map[string]string) {
	ConfigureInstanceWithProxy(ctx, ops, name, models, apiKeys, "")
}

// ConfigureInstanceWithProxy is like ConfigureInstance but accepts a proxy token.
// When proxyToken is non-empty and proxy is enabled, it writes proxy-redirected
// env vars instead of real keys.
func ConfigureInstanceWithProxy(ctx context.Context, ops InstanceOps, name string, models []string, apiKeys map[string]string, proxyToken string) {
	if len(models) == 0 && len(apiKeys) == 0 {
		return
	}

	// Wait for instance to become running
	if !waitForRunning(ctx, ops, name, 120*time.Second) {
		log.Printf("Timed out waiting for %s to start; models/keys not configured", name)
		return
	}

	// Write API keys as environment variables
	if len(apiKeys) > 0 {
		var lines []string

		if Cfg.ProxyEnabled && proxyToken != "" {
			// Proxy mode: write BASE_URL pointing to proxy + proxy token as API key
			proxyURL := Cfg.ProxyURL
			seenProviders := make(map[string]bool)
			for envVar := range apiKeys {
				provider := envVarToProvider(envVar)
				if provider == "" || seenProviders[provider] {
					continue
				}
				seenProviders[provider] = true
				baseURLEnv := providerToBaseURLEnv(provider)
				apiKeyEnv := providerToAPIKeyEnv(provider)
				if baseURLEnv != "" && apiKeyEnv != "" {
					lines = append(lines, fmt.Sprintf("%s=%s/v1/%s", baseURLEnv, proxyURL, provider))
					lines = append(lines, fmt.Sprintf("%s=%s", apiKeyEnv, proxyToken))
				}
			}
			// Also pass through BRAVE_API_KEY directly (not proxied)
			if braveKey, ok := apiKeys["BRAVE_API_KEY"]; ok {
				lines = append(lines, fmt.Sprintf("BRAVE_API_KEY=%s", braveKey))
			}
		} else {
			// Direct mode: write real keys
			for k, v := range apiKeys {
				lines = append(lines, fmt.Sprintf("%s=%s", k, v))
			}
		}

		data := []byte(strings.Join(lines, "\n") + "\n")
		if err := ops.WriteFile(ctx, name, pathClaworcKeys, data); err != nil {
			log.Printf("Error writing API keys for %s: %v", name, err)
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
			log.Printf("Error marshaling model config for %s: %v", name, err)
			return
		}
		cmd := []string{"su", "-", "claworc", "-c",
			fmt.Sprintf("openclaw config set agents.defaults.model '%s' --json", string(modelJSON))}
		_, stderr, code, err := ops.ExecInInstance(ctx, name, cmd)
		if err != nil {
			log.Printf("Error setting model config for %s: %v", name, err)
			return
		}
		if code != 0 {
			log.Printf("Failed to set model config for %s: %s", name, stderr)
			return
		}
	}

	// Restart gateway so it picks up new env vars and config
	cmd := []string{"su", "-", "claworc", "-c", "openclaw gateway stop"}
	_, stderr, code, err := ops.ExecInInstance(ctx, name, cmd)
	if err != nil {
		log.Printf("Error restarting gateway for %s: %v", name, err)
		return
	}
	if code != 0 {
		log.Printf("Failed to restart gateway for %s: %s", name, stderr)
		return
	}
	log.Printf("Models and API keys configured for %s", name)
}

// envVarToProvider maps API key env var names to proxy provider slugs.
func envVarToProvider(envVar string) string {
	mapping := map[string]string{
		"ANTHROPIC_API_KEY":  "anthropic",
		"OPENAI_API_KEY":     "openai",
		"GOOGLE_API_KEY":     "google",
		"GEMINI_API_KEY":     "google",
		"MISTRAL_API_KEY":    "mistral",
		"GROQ_API_KEY":       "groq",
		"DEEPSEEK_API_KEY":   "deepseek",
		"XAI_API_KEY":        "xai",
		"COHERE_API_KEY":     "cohere",
		"TOGETHER_API_KEY":   "together",
		"FIREWORKS_API_KEY":  "fireworks",
		"CEREBRAS_API_KEY":   "cerebras",
		"PERPLEXITY_API_KEY": "perplexity",
		"OPENROUTER_API_KEY": "openrouter",
		"OLLAMA_API_KEY":     "ollama",
	}
	return mapping[envVar]
}

// providerToBaseURLEnv maps provider slugs to the *_BASE_URL env var name.
func providerToBaseURLEnv(provider string) string {
	mapping := map[string]string{
		"anthropic":  "ANTHROPIC_BASE_URL",
		"openai":     "OPENAI_BASE_URL",
		"google":     "GOOGLE_API_BASE_URL",
		"mistral":    "MISTRAL_BASE_URL",
		"groq":       "GROQ_BASE_URL",
		"deepseek":   "DEEPSEEK_BASE_URL",
		"xai":        "XAI_BASE_URL",
		"cohere":     "COHERE_BASE_URL",
		"together":   "TOGETHER_BASE_URL",
		"fireworks":  "FIREWORKS_BASE_URL",
		"cerebras":   "CEREBRAS_BASE_URL",
		"perplexity": "PERPLEXITY_BASE_URL",
		"openrouter": "OPENROUTER_BASE_URL",
		"ollama":     "OLLAMA_BASE_URL",
	}
	return mapping[provider]
}

// providerToAPIKeyEnv maps provider slugs to the *_API_KEY env var name.
func providerToAPIKeyEnv(provider string) string {
	mapping := map[string]string{
		"anthropic":  "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"google":     "GOOGLE_API_KEY",
		"mistral":    "MISTRAL_API_KEY",
		"groq":       "GROQ_API_KEY",
		"deepseek":   "DEEPSEEK_API_KEY",
		"xai":        "XAI_API_KEY",
		"cohere":     "COHERE_API_KEY",
		"together":   "TOGETHER_API_KEY",
		"fireworks":  "FIREWORKS_API_KEY",
		"cerebras":   "CEREBRAS_API_KEY",
		"perplexity": "PERPLEXITY_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
		"ollama":     "OLLAMA_API_KEY",
	}
	return mapping[provider]
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
