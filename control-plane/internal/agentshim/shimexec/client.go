package shimexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	gossh "golang.org/x/crypto/ssh"
)

// AgentInfo is the agent identity block of the shim meta document.
type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// LLMMeta is the llm block of the shim meta document.
type LLMMeta struct {
	Styles []string `json:"styles"`
}

// Meta is the wire document printed by the `meta` verb (docs/shim.md).
// Unknown fields are ignored per the contract's forward-compatibility rule.
type Meta struct {
	Contract           int                    `json:"contract"`
	ShimVersion        string                 `json:"shim_version"`
	Agent              AgentInfo              `json:"agent"`
	Capabilities       []string               `json:"capabilities"`
	ConfigFiles        []agentshim.ConfigFile `json:"config_files"`
	WorkspaceDir       string                 `json:"workspace_dir"`
	SkillsDir          string                 `json:"skills_dir"`
	LogFiles           []agentshim.LogFile    `json:"log_files"`
	LLM                LLMMeta                `json:"llm"`
	SessionPersistence string                 `json:"session_persistence"`
	ChatEndDetection   string                 `json:"chat_end_detection,omitempty"`
}

// Has reports whether the meta document declares the given capability string.
func (m *Meta) Has(capability string) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// capabilities translates the meta document's capability strings into the
// adapter-agnostic Capabilities struct.
func (m *Meta) capabilities() agentshim.Capabilities {
	return agentshim.Capabilities{
		Chat:         m.Has("chat"),
		ChatAbort:    m.Has("chat.abort"),
		SessionReset: m.Has("session.reset"),
		Config:       m.Has("config"),
		ConfigureLLM: m.Has("configure-llm"),
		Restart:      m.Has("restart"),
		ControlUI:    m.Has("control-ui"),
		Skills:       m.Has("skills"),

		ConfigFiles:        m.ConfigFiles,
		WorkspaceDir:       m.WorkspaceDir,
		SkillsDir:          m.SkillsDir,
		LogFiles:           m.LogFiles,
		LLMStyles:          m.LLM.Styles,
		SessionPersistence: m.SessionPersistence,
	}
}

// Client implements agentshim.Client by invoking the shim verbs under
// /opt/claworc/shim through a Runner. The parsed meta document is fetched
// once and cached; InvalidateCache forces a re-probe (e.g. after an image
// update).
type Client struct {
	runner Runner

	// AbortGrace is how long Session.Abort waits for the in-flight
	// chat-send to exit after chat-abort before hard-terminating its
	// transport. Defaults to 5s.
	AbortGrace time.Duration

	mu     sync.Mutex
	cached *Meta
}

var _ agentshim.Client = (*Client)(nil)

// New builds a Client on top of the given Runner.
func New(runner Runner) *Client {
	return &Client{runner: runner, AbortGrace: 5 * time.Second}
}

// NewFromSSH builds a Client whose verbs run over SSH connections resolved
// by the given function (typically a closure over the sshproxy manager).
func NewFromSSH(resolve func(ctx context.Context) (*gossh.Client, error)) *Client {
	return New(NewSSHRunner(resolve))
}

// Type implements agentshim.Client.
func (c *Client) Type() string { return Type }

// run executes one shim verb, returning its stdout after mapping the
// contract exit codes to errors.
func (c *Client) run(ctx context.Context, stdin io.Reader, verb string, args ...string) (string, error) {
	argv := append([]string{verbPath(verb)}, args...)
	var out bytes.Buffer
	tail := newTailBuffer(stderrTailCap)
	code, err := c.runner.Run(ctx, argv, stdin, &out, tail)
	if err != nil {
		return out.String(), fmt.Errorf("%s: %w", verb, err)
	}
	if err := mapExit(verb, code, out.Bytes(), tail.String()); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// Meta returns the parsed (and validated) shim meta document, fetching it on
// first use. The result is a copy; mutating it does not affect the cache.
func (c *Client) Meta(ctx context.Context) (Meta, error) {
	m, err := c.getMeta(ctx)
	if err != nil {
		return Meta{}, err
	}
	return *m, nil
}

// getMeta returns the cached meta document, probing the shim when absent.
// The mutex is held across the probe so concurrent callers share one fetch.
func (c *Client) getMeta(ctx context.Context) (*Meta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil {
		return c.cached, nil
	}
	stdout, err := c.run(ctx, nil, "meta")
	if err != nil {
		return nil, err
	}
	m, err := parseMeta([]byte(stdout))
	if err != nil {
		return nil, err
	}
	c.cached = m
	return m, nil
}

// parseMeta decodes and validates a shim meta document.
func parseMeta(raw []byte) (*Meta, error) {
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("meta: invalid JSON: %w", err)
	}
	if m.Contract != SupportedContract {
		return nil, fmt.Errorf("meta: unsupported shim contract %d (control plane supports %d)", m.Contract, SupportedContract)
	}
	if !m.Has("chat") {
		return nil, fmt.Errorf("meta: shim does not declare the required %q capability", "chat")
	}
	return &m, nil
}

// InvalidateCache drops the cached meta document so the next call re-probes
// the shim (used after image updates and SSH reconnects).
func (c *Client) InvalidateCache() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

// Capabilities implements agentshim.Client.
func (c *Client) Capabilities(ctx context.Context) (agentshim.Capabilities, error) {
	m, err := c.getMeta(ctx)
	if err != nil {
		return agentshim.Capabilities{}, err
	}
	return m.capabilities(), nil
}

// Health implements agentshim.Client: `health` exit 0 → nil, 4 → ErrBooting,
// anything else → an error carrying the shim's diagnostics.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.run(ctx, nil, "health")
	return err
}

// Identity reads the static identity files (agent.txt, agent.svg). The name
// is the first line of agent.txt, trimmed. A missing or unreadable agent.svg
// is tolerated (nil bytes) — the UI falls back to a generic icon.
func (c *Client) Identity(ctx context.Context) (name string, svg []byte, err error) {
	txt, err := c.runner.ReadFile(ctx, ShimDir+"/agent.txt")
	if err != nil {
		return "", nil, fmt.Errorf("read agent.txt: %w", err)
	}
	name, _, _ = strings.Cut(string(txt), "\n")
	name = strings.TrimSpace(name)
	svg, svgErr := c.runner.ReadFile(ctx, ShimDir+"/agent.svg")
	if svgErr != nil {
		svg = nil
	}
	return name, svg, nil
}

// GetConfig implements agentshim.Client: `config-get --id <id>`, with the
// editor language taken from the meta config_files entry.
func (c *Client) GetConfig(ctx context.Context, fileID string) (string, string, error) {
	cf, err := c.findConfigFile(ctx, fileID)
	if err != nil {
		return "", "", err
	}
	content, err := c.run(ctx, nil, "config-get", "--id", cf.ID)
	if err != nil {
		return "", "", err
	}
	return content, cf.Language, nil
}

// SetConfig implements agentshim.Client: `config-set --id <id>` with the new
// content on stdin. A validation failure (exit 6) surfaces as
// *ValidationError carrying the shim's {"error":...} message. The agent is
// NOT restarted here (contract: config-set must not restart; callers invoke
// Restart when the file declares restart_required).
func (c *Client) SetConfig(ctx context.Context, fileID, content string) error {
	cf, err := c.findConfigFile(ctx, fileID)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, strings.NewReader(content), "config-set", "--id", cf.ID)
	return err
}

// findConfigFile resolves fileID ("" selects the first declared file)
// against the meta document's config_files.
func (c *Client) findConfigFile(ctx context.Context, fileID string) (*agentshim.ConfigFile, error) {
	m, err := c.getMeta(ctx)
	if err != nil {
		return nil, err
	}
	cf := m.capabilities().FindConfigFile(fileID)
	if cf == nil {
		if fileID == "" {
			return nil, fmt.Errorf("agent declares no config files")
		}
		return nil, fmt.Errorf("unknown config file %q", fileID)
	}
	return cf, nil
}

// Restart implements agentshim.Client: the `restart` verb.
func (c *Client) Restart(ctx context.Context) error {
	_, err := c.run(ctx, nil, "restart")
	return err
}

// llmWire mirrors the configure-llm routing document exactly as specified in
// docs/shim.md. agentshim.LLMRouting's JSON tags happen to match, but we
// marshal through this local wire struct so the on-the-wire shape is pinned
// to the contract (nil slices become [], model entries carry only "id")
// independently of future changes to the shared types.
type llmWire struct {
	ProxyURL       string            `json:"proxy_url"`
	Style          string            `json:"style"`
	DefaultModel   string            `json:"default_model"`
	FallbackModels []string          `json:"fallback_models"`
	Providers      []llmProviderWire `json:"providers"`
}

type llmProviderWire struct {
	Key    string `json:"key"`
	APIKey string `json:"api_key"`
	// api_type is not part of the documented routing schema; it is included
	// (omitempty) because shims MUST ignore unknown fields and dialect-aware
	// shims need it to distinguish e.g. codex-style providers.
	APIType string         `json:"api_type,omitempty"`
	Models  []llmModelWire `json:"models"`
}

type llmModelWire struct {
	ID string `json:"id"`
}

func buildLLMWire(routing agentshim.LLMRouting) llmWire {
	w := llmWire{
		ProxyURL:       routing.ProxyURL,
		Style:          routing.Style,
		DefaultModel:   routing.DefaultModel,
		FallbackModels: routing.FallbackModels,
		Providers:      make([]llmProviderWire, 0, len(routing.Providers)),
	}
	if w.FallbackModels == nil {
		w.FallbackModels = []string{}
	}
	for _, p := range routing.Providers {
		models := make([]llmModelWire, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, llmModelWire{ID: m.ID})
		}
		w.Providers = append(w.Providers, llmProviderWire{
			Key:     p.Key,
			APIKey:  p.APIKey,
			APIType: p.APIType,
			Models:  models,
		})
	}
	return w
}

// ConfigureLLM implements agentshim.Client: pipes the generic routing
// document into the `configure-llm` verb. Exit 6 (routing not expressible)
// surfaces as *ValidationError.
func (c *Client) ConfigureLLM(ctx context.Context, routing agentshim.LLMRouting) error {
	doc, err := json.Marshal(buildLLMWire(routing))
	if err != nil {
		return fmt.Errorf("configure-llm: marshal routing: %w", err)
	}
	_, err = c.run(ctx, strings.NewReader(string(doc)), "configure-llm")
	return err
}

// OpenSession implements agentshim.Client. It validates the shim probe (meta
// must parse and declare chat) and returns a Session whose turns each run
// one streaming `chat-send` exec.
func (c *Client) OpenSession(ctx context.Context, sessionKey string) (agentshim.Session, error) {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	if !caps.Chat {
		return nil, fmt.Errorf("open session: %w", ErrUnsupported)
	}
	return newSession(c, sessionKey), nil
}
