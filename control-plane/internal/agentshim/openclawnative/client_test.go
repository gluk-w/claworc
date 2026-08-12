package openclawnative

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
)

// mockExec records ExecOpenclaw calls.
type mockExec struct {
	mu    sync.Mutex
	calls [][]string
}

func (m *mockExec) ExecOpenclaw(_ context.Context, args ...string) (string, string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, args)
	return "", "", 0, nil
}

func TestConfigureLLM_CallSequence(t *testing.T) {
	exec := &mockExec{}
	routing := agentshim.LLMRouting{
		ProxyURL:       "http://127.0.0.1:40001",
		Style:          "openai",
		DefaultModel:   "anthropic/claude-sonnet-4-5",
		FallbackModels: []string{"openai/gpt-5"},
		Providers: []agentshim.ProviderRoute{
			{Key: "anthropic", APIKey: "vk-1", APIType: "anthropic-messages",
				Models: []agentshim.ModelRef{{ID: "claude-sonnet-4-5", Default: true}}},
		},
	}
	if err := NewWithExec(exec).ConfigureLLM(context.Background(), routing); err != nil {
		t.Fatalf("ConfigureLLM: %v", err)
	}

	want := [][]string{
		{"config", "set", "agents.defaults.model"},
		{"config", "unset", "agents.defaults.models"},
		{"config", "set", "agents.defaults.models"},
		{"config", "unset", "models.providers"},
		{"config", "set", "models.providers"},
		{"gateway", "stop"},
	}
	if len(exec.calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %v", len(exec.calls), len(want), exec.calls)
	}
	for i, w := range want {
		for j, arg := range w {
			if exec.calls[i][j] != arg {
				t.Errorf("call %d = %v, want prefix %v", i, exec.calls[i], w)
				break
			}
		}
	}

	// agents.defaults.model payload: primary + fallbacks
	var modelCfg map[string]any
	if err := json.Unmarshal([]byte(exec.calls[0][3]), &modelCfg); err != nil {
		t.Fatalf("model config not JSON: %v", err)
	}
	if modelCfg["primary"] != "anthropic/claude-sonnet-4-5" {
		t.Errorf("primary = %v", modelCfg["primary"])
	}
	fallbacks, _ := modelCfg["fallbacks"].([]any)
	if len(fallbacks) != 1 || fallbacks[0] != "openai/gpt-5" {
		t.Errorf("fallbacks = %v", modelCfg["fallbacks"])
	}

	// allowlist contains both models
	allowlist := exec.calls[2][3]
	if !strings.Contains(allowlist, "anthropic/claude-sonnet-4-5") || !strings.Contains(allowlist, "openai/gpt-5") {
		t.Errorf("allowlist = %s", allowlist)
	}
}

func TestBuildProvidersJSON(t *testing.T) {
	routing := agentshim.LLMRouting{
		ProxyURL: "http://127.0.0.1:40001",
		Providers: []agentshim.ProviderRoute{
			{Key: "anthropic", APIKey: "vk-abc", APIType: "anthropic-messages",
				Models: []agentshim.ModelRef{{ID: "claude-sonnet-4-5"}}},
			{Key: "custom", APIKey: "vk-def"}, // no api type → default; no models → []
		},
	}
	out, err := BuildProvidersJSON(routing)
	if err != nil {
		t.Fatalf("BuildProvidersJSON: %v", err)
	}
	var providers map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &providers); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	anth := providers["anthropic"]
	if anth["baseUrl"] != "http://127.0.0.1:40001" {
		t.Errorf("baseUrl = %v", anth["baseUrl"])
	}
	if anth["api"] != "anthropic-messages" {
		t.Errorf("api = %v", anth["api"])
	}
	if anth["apiKey"] != "vk-abc" {
		t.Errorf("apiKey = %v", anth["apiKey"])
	}

	custom := providers["custom"]
	if custom["api"] != "openai-completions" {
		t.Errorf("default api = %v, want openai-completions", custom["api"])
	}
	if models, ok := custom["models"].([]any); !ok || len(models) != 0 {
		t.Errorf("empty models must marshal as []: %v", custom["models"])
	}
}

// TestBuildProvidersJSON_CodexDeclaresOpenAIResponses guards the codex
// special case: the openai-codex-responses api type is declared to OpenClaw
// as openai-responses so pi-ai skips its client-side JWT decode of apiKey.
func TestBuildProvidersJSON_CodexDeclaresOpenAIResponses(t *testing.T) {
	routing := agentshim.LLMRouting{
		ProxyURL: "http://127.0.0.1:40001",
		Providers: []agentshim.ProviderRoute{
			{Key: "codex", APIKey: "vk-x", APIType: "openai-codex-responses"},
		},
	}
	out, err := BuildProvidersJSON(routing)
	if err != nil {
		t.Fatalf("BuildProvidersJSON: %v", err)
	}
	var providers map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &providers); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if providers["codex"]["api"] != "openai-responses" {
		t.Errorf("codex api = %v, want openai-responses", providers["codex"]["api"])
	}
}

func TestBuildProvidersJSON_Empty(t *testing.T) {
	if out, _ := BuildProvidersJSON(agentshim.LLMRouting{ProxyURL: "http://x"}); out != "" {
		t.Errorf("no providers must yield empty string, got %q", out)
	}
	if out, _ := BuildProvidersJSON(agentshim.LLMRouting{
		Providers: []agentshim.ProviderRoute{{Key: "a"}},
	}); out != "" {
		t.Errorf("no proxy URL must yield empty string, got %q", out)
	}
}

func TestBuildModelsJSON(t *testing.T) {
	if out := BuildModelsJSON(agentshim.LLMRouting{}); out != "" {
		t.Errorf("no default model must yield empty string, got %q", out)
	}
	out := BuildModelsJSON(agentshim.LLMRouting{DefaultModel: "m1"})
	if out != `{"fallbacks":[],"primary":"m1"}` {
		t.Errorf("BuildModelsJSON = %s", out)
	}
}

func TestCapabilities(t *testing.T) {
	caps, err := New(agentshim.InstanceDeps{}).Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Chat || !caps.ChatAbort || !caps.SessionReset || !caps.Config ||
		!caps.ConfigureLLM || !caps.Restart || !caps.ControlUI || !caps.Skills {
		t.Errorf("capability flags = %+v", caps)
	}
	file := caps.FindConfigFile("")
	if file == nil || file.Path != ConfigPath || file.Language != "json" || !file.RestartRequired {
		t.Errorf("config file = %+v", file)
	}
	if caps.SessionPersistence != "native" {
		t.Errorf("session persistence = %q", caps.SessionPersistence)
	}
	if len(caps.LLMStyles) != 1 || caps.LLMStyles[0] != "openai" {
		t.Errorf("llm styles = %v", caps.LLMStyles)
	}
}

// TestEventWireFormat pins the JSON the browser receives to the docs/shim.md
// JSONL schema.
func TestEventWireFormat(t *testing.T) {
	s := newSession(nil, "browser")
	s.translate(gwFrame("assistant", "r1", map[string]any{"text": "hi"}))

	cases := []struct {
		frame []byte
		want  string
	}{
		{
			gwFrame("lifecycle", "r1", map[string]any{"phase": "start"}),
			`{"v":1,"event":"start","session":"browser","turn":"r1"}`,
		},
		{
			gwFrame("assistant", "r1", map[string]any{"text": "hi there"}),
			`{"v":1,"event":"assistant","turn":"r1","message_id":"r1","text":"hi there"}`,
		},
		{
			gwFrame("lifecycle", "r1", map[string]any{"phase": "end"}),
			`{"v":1,"event":"end","turn":"r1","text":"hi there","stop_reason":"complete"}`,
		},
	}
	for _, c := range cases {
		ev, ok := s.translate(c.frame)
		if !ok {
			t.Fatalf("frame %s not translated", c.frame)
		}
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(b) != c.want {
			t.Errorf("wire JSON = %s\n            want %s", b, c.want)
		}
	}
}
