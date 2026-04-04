package handlers

import (
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// --- Pure function tests ---

func TestGenerateName_Basic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"My Agent", "bot-my-agent"},
		{"TEST_BOT", "bot-test-bot"},
		{"Hello World  123", "bot-hello-world-123"},
		{"special!@#chars", "bot-specialchars"},
		{"---leading---", "bot-leading"},
		{"a", "bot-a"},
	}
	for _, tt := range tests {
		got := generateName(tt.input)
		if got != tt.want {
			t.Errorf("generateName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateName_MaxLength(t *testing.T) {
	t.Parallel()
	long := ""
	for i := 0; i < 100; i++ {
		long += "a"
	}
	name := generateName(long)
	if len(name) > 63 {
		t.Errorf("name length = %d, want <= 63", len(name))
	}
}

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	got := formatTimestamp(ts)
	if got != "2026-01-15T10:30:00Z" {
		t.Errorf("formatTimestamp = %q, want %q", got, "2026-01-15T10:30:00Z")
	}
}

func TestFormatTimestamp_Zero(t *testing.T) {
	t.Parallel()
	got := formatTimestamp(time.Time{})
	if got != "" {
		t.Errorf("formatTimestamp(zero) = %q, want empty", got)
	}
}

func TestParseModelsConfig(t *testing.T) {
	t.Parallel()
	mc := parseModelsConfig(`{"disabled":["model-a"],"extra":["model-b"]}`)
	if len(mc.Disabled) != 1 || mc.Disabled[0] != "model-a" {
		t.Errorf("disabled = %v, want [model-a]", mc.Disabled)
	}
	if len(mc.Extra) != 1 || mc.Extra[0] != "model-b" {
		t.Errorf("extra = %v, want [model-b]", mc.Extra)
	}
}

func TestParseModelsConfig_Empty(t *testing.T) {
	t.Parallel()
	mc := parseModelsConfig("")
	if mc.Disabled == nil || mc.Extra == nil {
		t.Error("nil slices for empty config")
	}
}

func TestParseModelsConfig_Invalid(t *testing.T) {
	t.Parallel()
	mc := parseModelsConfig("not-json")
	if mc.Disabled == nil || mc.Extra == nil {
		t.Error("nil slices for invalid JSON")
	}
}

func TestComputeEffectiveModels(t *testing.T) {
	setupTestDB(t)
	database.SetSetting("default_models", `["gpt-4","claude-3","gemini"]`)

	mc := modelsConfig{
		Disabled: []string{"gemini"},
		Extra:    []string{"custom-model"},
	}
	effective := computeEffectiveModels(mc)

	// Should have gpt-4, claude-3 (gemini disabled), custom-model
	if len(effective) != 3 {
		t.Fatalf("len = %d, want 3, got %v", len(effective), effective)
	}
	if effective[0] != "gpt-4" || effective[1] != "claude-3" || effective[2] != "custom-model" {
		t.Errorf("effective = %v", effective)
	}
}

func TestComputeEffectiveModels_NoDefaults(t *testing.T) {
	setupTestDB(t)
	mc := modelsConfig{Extra: []string{"my-model"}}
	effective := computeEffectiveModels(mc)
	if len(effective) != 1 || effective[0] != "my-model" {
		t.Errorf("effective = %v, want [my-model]", effective)
	}
}

func TestParseEnabledProviders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"[]", 0},
		{"[1,2,3]", 3},
		{"garbage", 0},
	}
	for _, tt := range tests {
		got := parseEnabledProviders(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseEnabledProviders(%q) len = %d, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestResolveStatus(t *testing.T) {
	setupTestDB(t)

	tests := []struct {
		name       string
		dbStatus   string
		orchStatus string
		want       string
	}{
		{"running passes through", "running", "running", "running"},
		{"stopped passes through", "running", "stopped", "stopped"},
		{"creating stays creating", "creating", "stopped", "creating"},
		{"stopping+stopped resolves to stopped", "stopping", "stopped", "stopped"},
		{"stopping+running stays stopping", "stopping", "running", "stopping"},
		{"error+stopped becomes failed", "error", "stopped", "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := database.Instance{Status: tt.dbStatus}
			database.DB.Create(&inst)

			got := resolveStatus(&inst, tt.orchStatus)
			if got != tt.want {
				t.Errorf("resolveStatus(db=%q, orch=%q) = %q, want %q", tt.dbStatus, tt.orchStatus, got, tt.want)
			}
		})
	}
}

func TestGetEffectiveImage(t *testing.T) {
	setupTestDB(t)
	database.SetSetting("default_container_image", "glukw/openclaw-vnc-chromium:latest")

	// Instance with override
	inst := database.Instance{ContainerImage: "custom:v1"}
	if got := getEffectiveImage(inst); got != "custom:v1" {
		t.Errorf("with override: %q, want custom:v1", got)
	}

	// Instance without override — uses default
	inst2 := database.Instance{}
	if got := getEffectiveImage(inst2); got != "glukw/openclaw-vnc-chromium:latest" {
		t.Errorf("without override: %q, want default", got)
	}
}

func TestGetEffectiveResolution(t *testing.T) {
	setupTestDB(t)
	database.SetSetting("default_vnc_resolution", "1920x1080")

	inst := database.Instance{VNCResolution: "2560x1440"}
	if got := getEffectiveResolution(inst); got != "2560x1440" {
		t.Errorf("with override: %q", got)
	}

	inst2 := database.Instance{}
	if got := getEffectiveResolution(inst2); got != "1920x1080" {
		t.Errorf("without override: %q", got)
	}
}

func TestGetEffectiveTimezone(t *testing.T) {
	setupTestDB(t)
	database.SetSetting("default_timezone", "Europe/London")

	inst := database.Instance{Timezone: "Asia/Tokyo"}
	if got := getEffectiveTimezone(inst); got != "Asia/Tokyo" {
		t.Errorf("with override: %q", got)
	}

	inst2 := database.Instance{}
	if got := getEffectiveTimezone(inst2); got != "Europe/London" {
		t.Errorf("without override: %q", got)
	}
}

func TestGenerateToken_NotEmpty(t *testing.T) {
	t.Parallel()
	tok := generateToken()
	if tok == "" {
		t.Error("generateToken returned empty string")
	}
	if len(tok) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("token length = %d, want 64", len(tok))
	}
}

func TestGenerateToken_Unique(t *testing.T) {
	t.Parallel()
	t1 := generateToken()
	t2 := generateToken()
	if t1 == t2 {
		t.Error("two tokens are identical")
	}
}

func TestResolveInstanceModels(t *testing.T) {
	setupTestDB(t)
	database.SetSetting("default_models", `["gpt-4","claude-3"]`)

	inst := database.Instance{
		ModelsConfig: `{"disabled":[],"extra":["custom"]}`,
		DefaultModel: "claude-3",
	}
	models := resolveInstanceModels(inst)
	// claude-3 should be first since it's the default
	if len(models) < 1 || models[0] != "claude-3" {
		t.Errorf("default model not first: %v", models)
	}
}

func TestInstanceToResponse_MasksSensitiveFields(t *testing.T) {
	setupTestDB(t)
	database.SetSetting("default_models", `[]`)

	inst := database.Instance{
		Name:        "bot-test",
		DisplayName: "Test",
		Status:      "running",
		BraveAPIKey: "encrypted-value",
	}
	database.DB.Create(&inst)

	resp := instanceToResponse(inst, "running")

	// BraveAPIKey should NOT be in the response - only HasBraveOverride
	if !resp.HasBraveOverride {
		t.Error("HasBraveOverride should be true")
	}
	// Response should not contain the raw key anywhere
	// (the struct uses HasBraveOverride bool, not the key itself)
}

func TestStatusMessages(t *testing.T) {
	t.Parallel()
	setStatusMessage(99, "Creating container...")
	if got := getStatusMessage(99); got != "Creating container..." {
		t.Errorf("got %q, want %q", got, "Creating container...")
	}

	clearStatusMessage(99)
	if got := getStatusMessage(99); got != "" {
		t.Errorf("after clear: got %q, want empty", got)
	}

	// Non-existent
	if got := getStatusMessage(0); got != "" {
		t.Errorf("non-existent: got %q, want empty", got)
	}
}
