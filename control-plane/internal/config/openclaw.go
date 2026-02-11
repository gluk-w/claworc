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
// API keys are written to /etc/default/claworc-keys as KEY=VALUE lines,
// which the gateway service picks up via EnvironmentFile=.
//
// Models are set via `openclaw config set agents.defaults.model ... --json`.
func ConfigureInstance(ctx context.Context, ops InstanceOps, name string, models []string, apiKeys map[string]string) {
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
		for k, v := range apiKeys {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
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
