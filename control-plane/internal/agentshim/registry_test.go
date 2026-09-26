package agentshim_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/agentshim/openclawnative"
)

func TestRegistry_TypesOrdered(t *testing.T) {
	t.Parallel()
	entries := agentshim.Types()
	want := []string{"openclaw", "hermes", "nanoclaw", "custom"}
	if len(entries) != len(want) {
		t.Fatalf("Types() returned %d entries, want %d", len(entries), len(want))
	}
	for i, w := range want {
		if entries[i].Type != w {
			t.Errorf("Types()[%d].Type = %q, want %q", i, entries[i].Type, w)
		}
		if entries[i].DisplayName == "" {
			t.Errorf("Types()[%d] (%s) has empty DisplayName", i, entries[i].Type)
		}
		if entries[i].LogPath == "" {
			t.Errorf("Types()[%d] (%s) has empty LogPath", i, entries[i].Type)
		}
		if !entries[i].StaticCapabilities.Chat {
			t.Errorf("Types()[%d] (%s) must declare the chat capability", i, entries[i].Type)
		}
	}
}

func TestRegistry_Get(t *testing.T) {
	t.Parallel()
	// Empty string resolves to OpenClaw, mirroring EffectiveAgentType.
	entry, ok := agentshim.Get("")
	if !ok || entry.Type != agentshim.TypeOpenClaw {
		t.Fatalf("Get(\"\") = (%v, %v), want the openclaw entry", entry.Type, ok)
	}
	if _, ok := agentshim.Get("hermes"); !ok {
		t.Error("Get(hermes) not found")
	}
	if _, ok := agentshim.Get("bogus"); ok {
		t.Error("Get(bogus) unexpectedly found")
	}
}

func TestRegistry_Validate(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{"", "openclaw", "hermes", "nanoclaw", "custom"} {
		if err := agentshim.Validate(valid); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", valid, err)
		}
	}
	if err := agentshim.Validate("skynet"); err == nil {
		t.Error("Validate(skynet) = nil, want error")
	}
}

// TestRegistry_OpenClawMatchesNativeAdapter pins the static registry entry to
// the built-in OpenClaw adapter's own capability report so they cannot drift.
func TestRegistry_OpenClawMatchesNativeAdapter(t *testing.T) {
	t.Parallel()
	entry, _ := agentshim.Get(agentshim.TypeOpenClaw)
	if entry.Type != openclawnative.Type {
		t.Fatalf("registry openclaw type %q != adapter type %q", entry.Type, openclawnative.Type)
	}
	if !entry.HasControlUI {
		t.Error("openclaw entry must declare HasControlUI")
	}

	live, err := openclawnative.New(agentshim.InstanceDeps{}).Capabilities(context.Background())
	if err != nil {
		t.Fatalf("adapter capabilities: %v", err)
	}
	got := entry.StaticCapabilities
	for _, c := range []struct {
		name         string
		static, live bool
	}{
		{"Chat", got.Chat, live.Chat},
		{"ChatAbort", got.ChatAbort, live.ChatAbort},
		{"SessionReset", got.SessionReset, live.SessionReset},
		{"Config", got.Config, live.Config},
		{"ConfigureLLM", got.ConfigureLLM, live.ConfigureLLM},
		{"Restart", got.Restart, live.Restart},
		{"ControlUI", got.ControlUI, live.ControlUI},
		{"Skills", got.Skills, live.Skills},
	} {
		if c.static != c.live {
			t.Errorf("registry openclaw %s = %v, adapter reports %v", c.name, c.static, c.live)
		}
	}
}

func TestRegistry_ConservativeCapabilities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		agentType  string
		wantConfig bool
	}{
		{"hermes", true},
		{"nanoclaw", false},
		{"custom", true},
	}
	for _, tt := range tests {
		entry, ok := agentshim.Get(tt.agentType)
		if !ok {
			t.Fatalf("Get(%s) not found", tt.agentType)
		}
		caps := entry.StaticCapabilities
		if !caps.Chat || !caps.ConfigureLLM || !caps.Restart {
			t.Errorf("%s: chat/configure-llm/restart must all be true, got %+v", tt.agentType, caps)
		}
		if caps.Config != tt.wantConfig {
			t.Errorf("%s: Config = %v, want %v", tt.agentType, caps.Config, tt.wantConfig)
		}
		if entry.HasControlUI {
			t.Errorf("%s: only openclaw serves a control UI", tt.agentType)
		}
		if caps.ControlUI || caps.ChatAbort || caps.SessionReset || caps.Skills {
			t.Errorf("%s: conservative entry declares optional capabilities it cannot guarantee: %+v", tt.agentType, caps)
		}
	}
}

func TestRegistry_DefaultImage(t *testing.T) {
	t.Parallel()
	settings := map[string]string{
		"default_agent_image":  "claworc/openclaw:latest",
		"default_agent_images": `{"hermes":"claworc/hermes:latest","nanoclaw":"claworc/nanoclaw:latest","custom":""}`,
	}
	getSetting := func(key string) (string, error) {
		v, ok := settings[key]
		if !ok {
			return "", fmt.Errorf("setting %s not found", key)
		}
		return v, nil
	}

	tests := []struct {
		agentType string
		want      string
	}{
		{"", "claworc/openclaw:latest"},
		{"openclaw", "claworc/openclaw:latest"},
		{"hermes", "claworc/hermes:latest"},
		{"nanoclaw", "claworc/nanoclaw:latest"},
		{"custom", ""},
		{"unknown", ""},
	}
	for _, tt := range tests {
		if got := agentshim.DefaultImage(tt.agentType, getSetting); got != tt.want {
			t.Errorf("DefaultImage(%q) = %q, want %q", tt.agentType, got, tt.want)
		}
	}

	// Missing settings resolve to "" rather than erroring.
	empty := func(string) (string, error) { return "", fmt.Errorf("no settings") }
	if got := agentshim.DefaultImage("openclaw", empty); got != "" {
		t.Errorf("DefaultImage with no settings = %q, want empty", got)
	}
	if got := agentshim.DefaultImage("hermes", empty); got != "" {
		t.Errorf("DefaultImage with no settings = %q, want empty", got)
	}
}
