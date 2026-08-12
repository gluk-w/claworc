// Package agentshim is the universal interface between the Claworc control
// plane and the AI agent running inside an instance container. All
// agent-specific knowledge (protocols, config paths, CLI invocations) lives
// behind the Client/Session interfaces defined here; handlers talk only to
// this package, which in turn uses internal/sshproxy for transport.
//
// Layering (see docs/shim.md):
//
//	handlers / frontend
//	   └── internal/agentshim   (Client/Session interfaces + adapters)
//	          └── internal/sshproxy    (SSH exec / SFTP / tunnels — transport only)
//	                 └── internal/orchestrator  (container lifecycle only)
package agentshim

import (
	"context"
	"encoding/json"
)

// ConfigFile describes one agent config file exposed in the Config tab.
type ConfigFile struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	Language        string `json:"language"` // json | yaml | toml | ini | shell | plaintext
	Label           string `json:"label"`
	RestartRequired bool   `json:"restart_required"`
}

// LogFile describes one agent log file surfaced by log streaming.
type LogFile struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// Capabilities describes what the agent behind a Client supports. It mirrors
// the `meta` document of the shim contract (docs/shim.md).
type Capabilities struct {
	Chat         bool
	ChatAbort    bool
	SessionReset bool
	Config       bool
	ConfigureLLM bool
	Restart      bool
	ControlUI    bool
	Skills       bool

	ConfigFiles        []ConfigFile
	WorkspaceDir       string
	SkillsDir          string
	LogFiles           []LogFile
	LLMStyles          []string // "openai" and/or "anthropic"
	SessionPersistence string   // native | emulated | none
}

// FindConfigFile returns the config file with the given ID, or the first
// config file when id is empty. Returns nil when no file matches.
func (c Capabilities) FindConfigFile(id string) *ConfigFile {
	if len(c.ConfigFiles) == 0 {
		return nil
	}
	if id == "" {
		return &c.ConfigFiles[0]
	}
	for i := range c.ConfigFiles {
		if c.ConfigFiles[i].ID == id {
			return &c.ConfigFiles[i]
		}
	}
	return nil
}

// Client is the agent-agnostic handle for one instance's agent. It exposes
// every operation the control plane performs against an agent: chat sessions,
// config editing, LLM routing, restart, and health.
type Client interface {
	// Type identifies the adapter, e.g. "openclaw".
	Type() string
	// Capabilities reports what this agent supports.
	Capabilities(ctx context.Context) (Capabilities, error)
	// Health returns nil when the agent can take a chat turn.
	Health(ctx context.Context) error
	// GetConfig reads the config file identified by fileID ("" selects the
	// first declared file) and returns its content and editor language.
	GetConfig(ctx context.Context, fileID string) (content, language string, err error)
	// SetConfig replaces the config file's content. It does NOT restart the
	// agent — callers invoke Restart when the file declares RestartRequired.
	SetConfig(ctx context.Context, fileID, content string) error
	// Restart restarts the agent service.
	Restart(ctx context.Context) error
	// ConfigureLLM routes the agent's LLM traffic per the routing document.
	ConfigureLLM(ctx context.Context, routing LLMRouting) error
	// OpenSession opens a chat session for the opaque Claworc-chosen session
	// key (e.g. "browser", "claworc-webhook-<name>").
	OpenSession(ctx context.Context, sessionKey string) (Session, error)
}

// Session is one open chat channel to the agent. Sessions are not safe for
// concurrent Recv calls; Send/Abort/Reset may be called from another
// goroutine while Recv is blocked (matching websocket semantics).
type Session interface {
	// Send delivers one user message to the agent.
	Send(ctx context.Context, message string) error
	// Recv blocks until the next normalized chat event is available.
	Recv(ctx context.Context) (Event, error)
	// Abort aborts the in-flight turn, if any.
	Abort(ctx context.Context) error
	// Reset clears the session's conversation history.
	Reset(ctx context.Context) error
	// Close tears down the session's underlying transport.
	Close() error
}

// Event kinds, per the chat event JSONL schema in docs/shim.md.
const (
	EventStart     = "start"
	EventAssistant = "assistant"
	EventTool      = "tool"
	EventError     = "error"
	EventEnd       = "end"
)

// Stop reasons for EventEnd.
const (
	StopComplete = "complete"
	StopAborted  = "aborted"
	StopError    = "error"
)

// Event is one normalized chat event, matching the JSONL schema in
// docs/shim.md exactly. Serialized verbatim, it IS the browser chat protocol.
//
// IMPORTANT: Text on "assistant" events is a CUMULATIVE SNAPSHOT of the
// message identified by MessageID — the full text of that message so far, NOT
// a delta. Consumers must replace, never append. A turn may contain multiple
// MessageIDs (text → tool calls → more text); each snapshot replaces only its
// own message. Text on "end" events carries the final text of the last
// assistant message so one-shot consumers (webhooks) can ignore everything
// else.
type Event struct {
	V          int             `json:"v"`
	Kind       string          `json:"event"` // start | assistant | tool | error | end
	Session    string          `json:"session,omitempty"`
	Turn       string          `json:"turn,omitempty"`
	MessageID  string          `json:"message_id,omitempty"`
	Text       string          `json:"text,omitempty"`
	Name       string          `json:"name,omitempty"`  // tool events
	Phase      string          `json:"phase,omitempty"` // tool events: start | result
	Code       string          `json:"code,omitempty"`  // error events
	StopReason string          `json:"stop_reason,omitempty"`
	Fatal      bool            `json:"fatal,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// ModelRef references one model served by a provider route.
type ModelRef struct {
	ID      string `json:"id"`
	Default bool   `json:"default,omitempty"`
}

// ProviderRoute routes one provider's traffic through the LLM proxy using a
// virtual key.
type ProviderRoute struct {
	Key     string     `json:"key"`
	APIKey  string     `json:"api_key"`
	APIType string     `json:"api_type,omitempty"`
	Models  []ModelRef `json:"models"`
}

// LLMRouting mirrors the configure-llm routing document (docs/shim.md): it
// describes how to route all of the agent's LLM traffic through the Claworc
// LLM proxy using virtual keys.
type LLMRouting struct {
	ProxyURL       string          `json:"proxy_url"`
	Style          string          `json:"style"` // openai | anthropic
	DefaultModel   string          `json:"default_model"`
	FallbackModels []string        `json:"fallback_models"`
	Providers      []ProviderRoute `json:"providers"`
}

// Models returns the effective ordered model list: DefaultModel followed by
// FallbackModels. Empty when no default model is set.
func (r LLMRouting) Models() []string {
	if r.DefaultModel == "" {
		return nil
	}
	models := make([]string, 0, 1+len(r.FallbackModels))
	models = append(models, r.DefaultModel)
	models = append(models, r.FallbackModels...)
	return models
}

// TransportError wraps failures to reach the instance (SSH connection,
// tunnel lookup) as opposed to agent-level failures, so handlers can
// distinguish "cannot reach the container" (502) from "agent operation
// failed" (503/500).
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return "transport: " + e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }
