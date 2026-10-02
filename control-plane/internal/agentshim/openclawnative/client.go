// Package openclawnative implements the agentshim Client/Session interfaces
// for pre-shim OpenClaw images using OpenClaw's native machinery: the gateway
// WebSocket protocol for chat, the `openclaw` CLI over SSH for config/LLM
// routing, and direct SFTP file access for the config file.
package openclawnative

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"path"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/internalproxy"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	gossh "golang.org/x/crypto/ssh"
)

// Type is the agent type identifier for the native OpenClaw adapter.
const Type = "openclaw"

// ConfigPath is the OpenClaw config file location inside the instance.
const ConfigPath = "/home/claworc/.openclaw/openclaw.json"

// SkillsDir is the OpenClaw skills directory inside the instance.
const SkillsDir = "/home/claworc/.openclaw/skills"

// configFileID is the ID of the single config file OpenClaw exposes.
const configFileID = "main"

func init() {
	agentshim.RegisterAdapter(Type, func(deps agentshim.InstanceDeps) agentshim.Client {
		return New(deps)
	})
}

// Client is the native OpenClaw adapter.
type Client struct {
	deps agentshim.InstanceDeps
	// exec, when non-nil, overrides SSH-resolved CLI execution. Used by
	// callers that already hold an established connection (instance
	// create/clone flows) and by tests.
	exec Instance
}

var _ agentshim.Client = (*Client)(nil)

// New builds a Client from factory-resolved instance dependencies.
func New(deps agentshim.InstanceDeps) *Client { return &Client{deps: deps} }

// NewWithExec builds a Client whose CLI verbs run over an already-established
// Instance. Only exec-backed operations (ConfigureLLM, Restart) are
// usable on such a client; chat and config file access need factory deps.
func NewWithExec(exec Instance) *Client { return &Client{exec: exec} }

// Type implements agentshim.Client.
func (c *Client) Type() string { return Type }

// Capabilities implements agentshim.Client. OpenClaw's capabilities are
// static — the adapter is built into the control plane, no probe needed.
func (c *Client) Capabilities(_ context.Context) (agentshim.Capabilities, error) {
	return agentshim.Capabilities{
		Chat:         true,
		ChatAbort:    true,
		SessionReset: true,
		Config:       true,
		ConfigureLLM: true,
		Restart:      true,
		ControlUI:    true,
		Skills:       true,
		ConfigFiles: []agentshim.ConfigFile{{
			ID:              configFileID,
			Path:            ConfigPath,
			Language:        "json",
			Label:           "openclaw.json",
			RestartRequired: true,
		}},
		WorkspaceDir:       "/home/claworc/.openclaw/workspace",
		SkillsDir:          SkillsDir,
		LogFiles:           []agentshim.LogFile{{Path: "/var/log/claworc/openclaw.log", Label: "OpenClaw"}},
		LLMStyles:          []string{"openai"},
		SessionPersistence: "native",
	}, nil
}

// Health implements agentshim.Client: a cheap dial-ability check of the
// gateway tunnel. The tunnel only exists while SSH is up, and the gateway
// only listens while OpenClaw runs, so a successful TCP connect is a good
// readiness signal without the cost of a full WebSocket handshake.
func (c *Client) Health(_ context.Context) error {
	port, err := c.tunnelPort()
	if err != nil {
		return &agentshim.TransportError{Err: err}
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		return fmt.Errorf("gateway not reachable: %w", err)
	}
	conn.Close()
	return nil
}

// GetConfig implements agentshim.Client: reads openclaw.json over SFTP.
func (c *Client) GetConfig(ctx context.Context, fileID string) (string, string, error) {
	if err := checkFileID(fileID); err != nil {
		return "", "", err
	}
	client, err := c.sshClient(ctx)
	if err != nil {
		return "", "", &agentshim.TransportError{Err: err}
	}
	content, err := sshproxy.ReadFile(client, ConfigPath)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", ConfigPath, err)
	}
	return string(content), "json", nil
}

// SetConfig implements agentshim.Client: writes openclaw.json over SFTP.
// The agent is NOT restarted here; callers invoke Restart afterwards
// (the file declares RestartRequired).
func (c *Client) SetConfig(ctx context.Context, fileID, content string) error {
	if err := checkFileID(fileID); err != nil {
		return err
	}
	client, err := c.sshClient(ctx)
	if err != nil {
		return &agentshim.TransportError{Err: err}
	}
	if err := sshproxy.WriteFile(client, ConfigPath, []byte(content)); err != nil {
		return fmt.Errorf("write %s: %w", ConfigPath, err)
	}
	return nil
}

// DeploySkill implements agentshim.Client: syncs the skill's files into
// SkillsDir over SFTP-style writes (files end up claworc-owned).
func (c *Client) DeploySkill(ctx context.Context, name string, files map[string][]byte) error {
	base, err := skillBase(name)
	if err != nil {
		return err
	}
	client, err := c.sshClient(ctx)
	if err != nil {
		return &agentshim.TransportError{Err: err}
	}
	if err := sshproxy.CreateDirectory(client, base); err != nil {
		return fmt.Errorf("create skill directory: %w", err)
	}
	for rel, data := range files {
		rel = path.Clean(rel)
		if !agentshim.SkillPathSafe(rel) {
			return fmt.Errorf("invalid skill file path %q", rel)
		}
		remote := path.Join(base, rel)
		if parent := path.Dir(remote); parent != base {
			if err := sshproxy.CreateDirectory(client, parent); err != nil {
				return fmt.Errorf("create directory %s: %w", parent, err)
			}
		}
		if err := sshproxy.WriteFile(client, remote, data); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// RemoveSkill implements agentshim.Client: deletes <SkillsDir>/<name>.
// Removing a skill that was never deployed is not an error.
func (c *Client) RemoveSkill(ctx context.Context, name string) error {
	base, err := skillBase(name)
	if err != nil {
		return err
	}
	client, err := c.sshClient(ctx)
	if err != nil {
		return &agentshim.TransportError{Err: err}
	}
	return sshproxy.DeletePath(client, base)
}

// skillBase joins a validated skill name under SkillsDir.
func skillBase(name string) (string, error) {
	if !agentshim.SkillPathSafe(name) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	return path.Join(SkillsDir, name), nil
}

// Restart implements agentshim.Client: `openclaw gateway stop` — s6
// supervises the gateway and restarts it immediately with fresh config.
func (c *Client) Restart(ctx context.Context) error {
	inst, err := c.execInstance(ctx)
	if err != nil {
		return err
	}
	if _, stderr, code, err := inst.ExecOpenclaw(ctx, "gateway", "stop"); err != nil || code != 0 {
		return fmt.Errorf("restart gateway: %v %s", err, stderr)
	}
	return nil
}

// ConfigureLLM implements agentshim.Client. It translates the generic
// routing document into OpenClaw's native config via `openclaw config
// set/unset --json` and restarts the gateway:
//
//   - agents.defaults.model  — primary + fallbacks
//   - agents.defaults.models — allowlist restricting the UI model dropdown
//   - models.providers       — providers pointing baseUrl at the LLM proxy
//     with virtual keys
//
// Error semantics mirror the historical ConfigureInstance behavior: a
// transport-level exec failure aborts (and is returned); a non-zero exit from
// a single config step is logged and the remaining steps still run, so a bad
// model name can never leave providers unconfigured.
func (c *Client) ConfigureLLM(ctx context.Context, routing agentshim.LLMRouting) error {
	inst, err := c.execInstance(ctx)
	if err != nil {
		return err
	}

	name := c.deps.Instance.Name

	models := routing.Models()
	if len(models) > 0 {
		modelJSON := BuildModelsJSON(routing)
		_, stderr, code, err := inst.ExecOpenclaw(ctx, "config", "set", "agents.defaults.model", modelJSON, "--json")
		if err != nil {
			return fmt.Errorf("set agents.defaults.model: %w", err)
		}
		if code != 0 {
			log.Printf("[openclawnative] %s: set agents.defaults.model failed: %s", name, utils.SanitizeForLog(stderr))
			// continue — providers must still be configured even if model config failed
		}

		// Set the models allowlist to restrict the UI dropdown to only
		// configured models. `openclaw config set` deep-merges into existing
		// map values, so a previously-selected model that the admin
		// de-selected would linger — clear the path before writing.
		modelsMap := make(map[string]interface{}, len(models))
		for _, m := range models {
			modelsMap[m] = map[string]interface{}{}
		}
		modelsMapJSON, err := json.Marshal(modelsMap)
		if err != nil {
			log.Printf("[openclawnative] %s: marshal models allowlist: %v", name, err)
		} else {
			_, _, _, _ = inst.ExecOpenclaw(ctx, "config", "unset", "agents.defaults.models")
			_, stderr, code, err := inst.ExecOpenclaw(ctx, "config", "set", "agents.defaults.models", string(modelsMapJSON), "--json")
			if err != nil {
				log.Printf("[openclawnative] %s: set models allowlist: %v", name, err)
			} else if code != 0 {
				log.Printf("[openclawnative] %s: set models allowlist failed: %s", name, utils.SanitizeForLog(stderr))
			}
		}
	}

	if len(routing.Providers) > 0 && routing.ProxyURL != "" {
		providersJSON, err := BuildProvidersJSON(routing)
		if err != nil {
			log.Printf("[openclawnative] %s: marshal providers: %v", name, err)
		} else if providersJSON != "" {
			// Clear the providers map first so de-selected providers are
			// removed instead of being deep-merged with the previous config.
			_, _, _, _ = inst.ExecOpenclaw(ctx, "config", "unset", "models.providers")
			stdout, stderr, code, err := inst.ExecOpenclaw(ctx, "config", "set", "models.providers", providersJSON, "--json")
			if err != nil {
				log.Printf("[openclawnative] %s: set providers: %v", name, err)
			} else if code != 0 {
				log.Printf("[openclawnative] %s: set providers failed: stdout=%q stderr=%q", name, utils.SanitizeForLog(stdout), utils.SanitizeForLog(stderr))
			}
		}
	}

	// Restart the gateway so it picks up new env vars and config.
	stdout, stderr, code, err := inst.ExecOpenclaw(ctx, "gateway", "stop")
	if err != nil {
		return fmt.Errorf("restart gateway: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("restart gateway: stdout=%q stderr=%q", stdout, stderr)
	}
	return nil
}

// providerCfg is the JSON shape expected by OpenClaw's models.providers config.
type providerCfg struct {
	BaseURL string     `json:"baseUrl"`
	API     string     `json:"api"`
	APIKey  string     `json:"apiKey"`
	Models  []modelCfg `json:"models"`
}

type modelCfg struct {
	ID string `json:"id"`
}

// BuildProvidersJSON translates the routing document into OpenClaw's
// models.providers JSON. Returns "" when there is nothing to configure.
// Exported because the instance-create path also embeds this JSON in the
// OPENCLAW_INITIAL_PROVIDERS boot env var.
func BuildProvidersJSON(routing agentshim.LLMRouting) (string, error) {
	if len(routing.Providers) == 0 || routing.ProxyURL == "" {
		return "", nil
	}
	providers := make(map[string]providerCfg, len(routing.Providers))
	for _, p := range routing.Providers {
		apiType := p.APIType
		if apiType == "" {
			apiType = "openai-completions"
		}
		// Codex declares openai-responses to OpenClaw so pi-ai skips its
		// client-side JWT decode of apiKey. The gateway translates
		// path/auth/SSE upstream. The routing document keeps the codex
		// api type for gateway routing.
		if apiType == internalproxy.APITypeOpenAICodexResponses {
			apiType = "openai-responses"
		}
		models := make([]modelCfg, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, modelCfg{ID: m.ID})
		}
		providers[p.Key] = providerCfg{
			BaseURL: routing.ProxyURL,
			API:     apiType,
			APIKey:  p.APIKey,
			Models:  models,
		}
	}
	b, err := json.Marshal(providers)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BuildModelsJSON translates the routing document into OpenClaw's
// agents.defaults.model JSON ({"primary": ..., "fallbacks": [...]}).
// Returns "" when no default model is set. Exported because the
// instance-create path also embeds this JSON in the OPENCLAW_INITIAL_MODELS
// boot env var.
func BuildModelsJSON(routing agentshim.LLMRouting) string {
	if routing.DefaultModel == "" {
		return ""
	}
	fallbacks := routing.FallbackModels
	if fallbacks == nil {
		fallbacks = []string{}
	}
	b, err := json.Marshal(map[string]interface{}{
		"primary":   routing.DefaultModel,
		"fallbacks": fallbacks,
	})
	if err != nil {
		return ""
	}
	return string(b)
}

func checkFileID(fileID string) error {
	if fileID != "" && fileID != configFileID {
		return fmt.Errorf("unknown config file %q", fileID)
	}
	return nil
}

func (c *Client) tunnelPort() (int, error) {
	if c.deps.TunnelPort == nil {
		return 0, fmt.Errorf("openclawnative: no tunnel port resolver")
	}
	return c.deps.TunnelPort("gateway")
}

func (c *Client) sshClient(ctx context.Context) (*gossh.Client, error) {
	if c.deps.SSHClient == nil {
		return nil, fmt.Errorf("openclawnative: no SSH client resolver")
	}
	return c.deps.SSHClient(ctx)
}

func (c *Client) execInstance(ctx context.Context) (Instance, error) {
	if c.exec != nil {
		return c.exec, nil
	}
	if c.deps.SSHClient == nil {
		return nil, fmt.Errorf("openclawnative: no SSH client resolver")
	}
	client, err := c.deps.SSHClient(ctx)
	if err != nil {
		return nil, &agentshim.TransportError{Err: err}
	}
	return NewSSHInstance(client), nil
}

// ControlUI implements agentshim.Client: the OpenClaw gateway serves its
// Control UI under /openclaw/<instance id>/ on the gateway port (the claworc
// image sets gateway.controlUi.basePath to it; older images ignore the prefix
// and the proxy retries at the root), authenticated by the
// gateway token and a localhost Origin.
func (c *Client) ControlUI(_ context.Context) (agentshim.ControlUISpec, error) {
	spec := agentshim.ControlUISpec{
		Port:     GatewayPort,
		BasePath: fmt.Sprintf("/openclaw/%d/", c.deps.Instance.ID),
		Headers:  map[string]string{"Origin": fmt.Sprintf("http://localhost:%d", GatewayPort)},
	}
	if c.deps.GatewayToken != "" {
		spec.Query = map[string]string{"token": c.deps.GatewayToken}
	}
	return spec, nil
}

// GatewayPort is the OpenClaw gateway's container-local port.
const GatewayPort = 18789

// Legacy env vars read by pre-shim OpenClaw images at first boot. Shim images
// read CLAWORC_INITIAL_LLM_CONFIG / CLAWORC_AGENT_TOKEN instead, but an
// OpenClaw instance's image may be either, so both are always injected.
func init() {
	agentshim.RegisterLegacyEnv(agentshim.TypeOpenClaw,
		[]string{"OPENCLAW_GATEWAY_TOKEN", "OPENCLAW_INITIAL_MODELS", "OPENCLAW_INITIAL_PROVIDERS"},
		func(routing agentshim.LLMRouting, agentToken string) map[string]string {
			env := map[string]string{}
			if agentToken != "" {
				env["OPENCLAW_GATEWAY_TOKEN"] = agentToken
			}
			if modelsJSON := BuildModelsJSON(routing); modelsJSON != "" {
				env["OPENCLAW_INITIAL_MODELS"] = modelsJSON
			}
			if providersJSON, _ := BuildProvidersJSON(routing); providersJSON != "" {
				env["OPENCLAW_INITIAL_PROVIDERS"] = providersJSON
			}
			return env
		})
}
