package database

import "testing"

func TestParseSharedFolderInstanceIDs_Empty(t *testing.T) {
	t.Parallel()
	ids := ParseSharedFolderInstanceIDs("")
	if len(ids) != 0 {
		t.Errorf("empty string: got %v, want empty", ids)
	}
}

func TestParseSharedFolderInstanceIDs_EmptyArray(t *testing.T) {
	t.Parallel()
	ids := ParseSharedFolderInstanceIDs("[]")
	if len(ids) != 0 {
		t.Errorf("empty array: got %v, want empty", ids)
	}
}

func TestParseSharedFolderInstanceIDs_ValidJSON(t *testing.T) {
	t.Parallel()
	ids := ParseSharedFolderInstanceIDs("[1,2,3]")
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("valid JSON: got %v, want [1 2 3]", ids)
	}
}

func TestParseSharedFolderInstanceIDs_InvalidJSON(t *testing.T) {
	t.Parallel()
	ids := ParseSharedFolderInstanceIDs("not-json")
	if len(ids) != 0 {
		t.Errorf("invalid JSON: got %v, want empty", ids)
	}
}

func TestEncodeSharedFolderInstanceIDs_Empty(t *testing.T) {
	t.Parallel()
	result := EncodeSharedFolderInstanceIDs([]uint{})
	if result != "[]" {
		t.Errorf("empty slice: got %q, want %q", result, "[]")
	}
}

func TestEncodeSharedFolderInstanceIDs_Nil(t *testing.T) {
	t.Parallel()
	result := EncodeSharedFolderInstanceIDs(nil)
	if result != "[]" {
		t.Errorf("nil slice: got %q, want %q", result, "[]")
	}
}

func TestEncodeSharedFolderInstanceIDs_Values(t *testing.T) {
	t.Parallel()
	result := EncodeSharedFolderInstanceIDs([]uint{5, 10})
	if result != "[5,10]" {
		t.Errorf("got %q, want %q", result, "[5,10]")
	}
}

func TestParseProviderModels_Empty(t *testing.T) {
	t.Parallel()
	models := ParseProviderModels("")
	if len(models) != 0 {
		t.Errorf("empty: got %d models, want 0", len(models))
	}
}

func TestParseProviderModels_EmptyArray(t *testing.T) {
	t.Parallel()
	models := ParseProviderModels("[]")
	if len(models) != 0 {
		t.Errorf("empty array: got %d models, want 0", len(models))
	}
}

func TestParseProviderModels_ValidJSON(t *testing.T) {
	t.Parallel()
	raw := `[{"id":"claude-3","name":"Claude 3"},{"id":"gpt-4","name":"GPT-4"}]`
	models := ParseProviderModels(raw)
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "claude-3" || models[1].ID != "gpt-4" {
		t.Errorf("unexpected model IDs: %s, %s", models[0].ID, models[1].ID)
	}
}

func TestParseProviderModels_InvalidJSON(t *testing.T) {
	t.Parallel()
	models := ParseProviderModels("garbage")
	if len(models) != 0 {
		t.Errorf("invalid JSON: got %d models, want 0", len(models))
	}
}

func TestIsLegacyEmbedded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		image string
		want  bool
	}{
		{"glukw/openclaw-vnc-chromium:latest", true},
		{"glukw/openclaw-vnc-chrome:v1.2.3", true},
		{"glukw/openclaw-vnc-brave:latest", true},
		{"docker.io/glukw/openclaw-vnc-chromium:latest", true},
		{"glukw/claworc-agent:latest", false},
		{"glukw/claworc-browser-chromium:latest", false},
		{"", true},
		{"random/image:tag", false},
	}
	for _, tc := range cases {
		got := IsLegacyEmbedded(tc.image)
		if got != tc.want {
			t.Errorf("IsLegacyEmbedded(%q) = %v, want %v", tc.image, got, tc.want)
		}
	}
}

func TestParseEncodeSharedFolderInstanceIDs_Roundtrip(t *testing.T) {
	t.Parallel()
	original := []uint{1, 5, 100}
	encoded := EncodeSharedFolderInstanceIDs(original)
	decoded := ParseSharedFolderInstanceIDs(encoded)
	if len(decoded) != len(original) {
		t.Fatalf("roundtrip len = %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("roundtrip[%d] = %d, want %d", i, decoded[i], original[i])
		}
	}
}
