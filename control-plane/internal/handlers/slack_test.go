package handlers

import (
	"encoding/json"
	"testing"
)

func TestValidateSlackConfigNormalizesChannelIDs(t *testing.T) {
	f := false
	cfg := instanceSlackConfig{
		Enabled: true,
		Channels: []slackChannelEntry{
			{ID: " #c0123456789 "},
			{ID: "C0123456789", RequireMention: &f}, // duplicate after normalization
			{ID: "G9876543210"},
		},
	}
	if err := validateSlackConfig(&cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Channels) != 2 {
		t.Fatalf("expected duplicate channel to be dropped, got %d entries", len(cfg.Channels))
	}
	if cfg.Channels[0].ID != "C0123456789" || cfg.Channels[1].ID != "G9876543210" {
		t.Fatalf("unexpected normalized IDs: %+v", cfg.Channels)
	}
}

func TestValidateSlackConfigRejectsChannelNames(t *testing.T) {
	for _, bad := range []string{"general", "#general", "my-channel", "D0123456789", ""} {
		cfg := instanceSlackConfig{Enabled: true, Channels: []slackChannelEntry{{ID: bad}}}
		if err := validateSlackConfig(&cfg); err == nil {
			t.Errorf("expected error for channel ID %q", bad)
		}
	}
}

func TestValidateSlackConfigRejectsBadDMPolicy(t *testing.T) {
	cfg := instanceSlackConfig{Enabled: true, DMPolicy: "allowlist"}
	if err := validateSlackConfig(&cfg); err == nil {
		t.Error("expected error for unsupported dm_policy")
	}
}

func TestRenderSlackChannelsJSON(t *testing.T) {
	f := false
	cfg := instanceSlackConfig{
		Enabled: true,
		Channels: []slackChannelEntry{
			{ID: "C0123456789"},
			{ID: "G9876543210", RequireMention: &f},
		},
		DMPolicy: "open",
	}
	rendered, err := renderSlackChannelsJSON(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var block map[string]interface{}
	if err := json.Unmarshal([]byte(rendered), &block); err != nil {
		t.Fatalf("rendered config is not valid JSON: %v", err)
	}
	if block["enabled"] != true || block["groupPolicy"] != "allowlist" {
		t.Fatalf("unexpected top-level block: %v", block)
	}
	channels, ok := block["channels"].(map[string]interface{})
	if !ok || len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %v", block["channels"])
	}
	c1 := channels["C0123456789"].(map[string]interface{})
	if c1["requireMention"] != true {
		t.Errorf("nil RequireMention should render as true (OpenClaw default), got %v", c1)
	}
	c2 := channels["G9876543210"].(map[string]interface{})
	if c2["requireMention"] != false {
		t.Errorf("explicit false RequireMention should render as false, got %v", c2)
	}
	if block["dmPolicy"] != "open" {
		t.Errorf("expected dmPolicy open, got %v", block["dmPolicy"])
	}
	allowFrom, ok := block["allowFrom"].([]interface{})
	if !ok || len(allowFrom) != 1 || allowFrom[0] != "*" {
		t.Errorf("open dmPolicy must render allowFrom [\"*\"], got %v", block["allowFrom"])
	}
}

func TestRenderSlackChannelsJSONDisabled(t *testing.T) {
	rendered, err := renderSlackChannelsJSON(instanceSlackConfig{
		Enabled:  false,
		Channels: []slackChannelEntry{{ID: "C0123456789"}},
		DMPolicy: "open",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rendered != `{"enabled":false}` {
		t.Fatalf("disabled config must render only the enabled flag, got %s", rendered)
	}
}
