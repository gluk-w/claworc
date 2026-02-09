package orchestrator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	PathOpenClawConfig = "/home/claworc/.openclaw/openclaw.json"
)

var cmdGatewayStop = []string{"su", "-", "claworc", "-c", "openclaw gateway stop"}

// ExecFunc matches the ExecInInstance method signature.
type ExecFunc func(ctx context.Context, name string, cmd []string) (string, string, int, error)

// WaitFunc waits for an instance to become ready before exec is possible.
type WaitFunc func(ctx context.Context, name string, timeout time.Duration) bool

func configureGatewayToken(ctx context.Context, execFn ExecFunc, name, token string, waitFn WaitFunc) {
	if !waitFn(ctx, name, 120*time.Second) {
		log.Printf("Timed out waiting for %s to start; gateway token not configured", name)
		return
	}
	cmd := []string{"su", "-", "claworc", "-c", fmt.Sprintf("openclaw config set gateway.auth.token %s", token)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		log.Printf("Error configuring gateway token for %s: %v", name, err)
		return
	}
	if code != 0 {
		log.Printf("Failed to configure gateway token for %s: %s", name, stderr)
		return
	}
	_, stderr, code, err = execFn(ctx, name, cmdGatewayStop)
	if err != nil {
		log.Printf("Error restarting gateway for %s: %v", name, err)
		return
	}
	if code != 0 {
		log.Printf("Failed to restart gateway for %s: %s", name, stderr)
		return
	}
	log.Printf("Gateway token configured for %s", name)
}

// configureModelsAndKeys applies model config and API keys to the instance's openclaw.json.
// It reads the current config, merges model settings, sets API keys via `openclaw config set`,
// and restarts the gateway.
func configureModelsAndKeys(ctx context.Context, execFn ExecFunc, name string, models []string, apiKeys map[string]string, defaultProvider string, waitFn WaitFunc) {
	if len(models) == 0 && len(apiKeys) == 0 && defaultProvider == "" {
		return
	}

	if !waitFn(ctx, name, 120*time.Second) {
		log.Printf("Timed out waiting for %s to start; models/keys not configured", name)
		return
	}

	// Apply model config and default provider to openclaw.json
	if len(models) > 0 || defaultProvider != "" {
		// Read current config
		stdout, _, code, err := execFn(ctx, name, []string{"cat", PathOpenClawConfig})
		if err != nil || code != 0 {
			log.Printf("Failed to read openclaw.json for %s, using empty config", name)
			stdout = "{}"
		}

		var config map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &config); err != nil {
			config = make(map[string]interface{})
		}

		agents, ok := config["agents"].(map[string]interface{})
		if !ok {
			agents = make(map[string]interface{})
		}
		defaults, ok := agents["defaults"].(map[string]interface{})
		if !ok {
			defaults = make(map[string]interface{})
		}

		// Set agents.defaults.model.primary and fallbacks
		if len(models) > 0 {
			modelConfig := map[string]interface{}{
				"primary": models[0],
			}
			if len(models) > 1 {
				modelConfig["fallbacks"] = models[1:]
			} else {
				modelConfig["fallbacks"] = []string{}
			}
			defaults["model"] = modelConfig
		}

		// Set agents.defaults.provider (default provider key name)
		if defaultProvider != "" {
			defaults["provider"] = defaultProvider
		} else {
			delete(defaults, "provider")
		}

		agents["defaults"] = defaults
		config["agents"] = agents

		configBytes, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			log.Printf("Error marshaling config for %s: %v", name, err)
			return
		}

		b64 := base64.StdEncoding.EncodeToString(configBytes)
		cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > %s", b64, PathOpenClawConfig)}
		_, stderr, code, err := execFn(ctx, name, cmd)
		if err != nil {
			log.Printf("Error writing config for %s: %v", name, err)
			return
		}
		if code != 0 {
			log.Printf("Failed to write config for %s: %s", name, stderr)
			return
		}
	}

	// Set API keys via openclaw config set
	for keyName, keyValue := range apiKeys {
		cmd := []string{"su", "-", "claworc", "-c", fmt.Sprintf("openclaw config set env.%s %s", keyName, keyValue)}
		_, stderr, code, err := execFn(ctx, name, cmd)
		if err != nil {
			log.Printf("Error setting API key %s for %s: %v", keyName, name, err)
			continue
		}
		if code != 0 {
			log.Printf("Failed to set API key %s for %s: %s", keyName, name, stderr)
		}
	}

	// Restart gateway
	_, stderr, code, err := execFn(ctx, name, cmdGatewayStop)
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

func updateInstanceConfig(ctx context.Context, execFn ExecFunc, name string, configJSON string) error {
	b64 := base64.StdEncoding.EncodeToString([]byte(configJSON))
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > %s", b64, PathOpenClawConfig)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("write config: %s", stderr)
	}

	_, stderr, code, err = execFn(ctx, name, cmdGatewayStop)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("restart gateway: %s", stderr)
	}
	return nil
}

func listDirectory(ctx context.Context, execFn ExecFunc, name string, path string) ([]FileEntry, error) {
	stdout, stderr, code, err := execFn(ctx, name, []string{"ls", "-la", "--color=never", path})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("list directory: %s", stderr)
	}
	return ParseLsOutput(stdout), nil
}

func readFile(ctx context.Context, execFn ExecFunc, name string, path string) ([]byte, error) {
	stdout, stderr, code, err := execFn(ctx, name, []string{"cat", path})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("read file: %s", stderr)
	}
	return []byte(stdout), nil
}

func createFile(ctx context.Context, execFn ExecFunc, name string, path string, content string) error {
	escaped := strings.ReplaceAll(content, "'", "'\\''")
	cmd := []string{"sh", "-c", fmt.Sprintf("echo -n '%s' > '%s'", escaped, path)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("create file: %s", stderr)
	}
	return nil
}

func createDirectory(ctx context.Context, execFn ExecFunc, name string, path string) error {
	_, stderr, code, err := execFn(ctx, name, []string{"mkdir", "-p", path})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("create directory: %s", stderr)
	}
	return nil
}

func writeFile(ctx context.Context, execFn ExecFunc, name string, path string, data []byte) error {
	b64 := base64.StdEncoding.EncodeToString(data)
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > '%s'", b64, path)}
	_, stderr, code, err := execFn(ctx, name, cmd)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("write file: %s", stderr)
	}
	return nil
}

// ParseLsOutput parses the output of `ls -la` into FileEntry slices.
func ParseLsOutput(output string) []FileEntry {
	var entries []FileEntry
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= 1 {
		return entries
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 9 {
			continue
		}
		permissions := parts[0]
		size := parts[4]
		entryName := strings.Join(parts[8:], " ")

		if entryName == "." || entryName == ".." {
			continue
		}

		isDir := strings.HasPrefix(permissions, "d")
		isLink := strings.HasPrefix(permissions, "l")

		entryType := "file"
		if isDir {
			entryType = "directory"
		} else if isLink {
			entryType = "symlink"
		}

		var sizePtr *string
		if !isDir {
			sizePtr = &size
		}

		entries = append(entries, FileEntry{
			Name:        entryName,
			Type:        entryType,
			Size:        sizePtr,
			Permissions: permissions,
		})
	}
	return entries
}
