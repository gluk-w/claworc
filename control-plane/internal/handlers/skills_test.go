package handlers

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// sampleSkillPath returns the absolute path to skills/sampleskill/SKILL.md,
// resolved relative to this test file so it does not depend on CWD.
func sampleSkillPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = .../claworc/control-plane/internal/handlers/skills_test.go
	// target   = .../claworc/skills/sampleskill/SKILL.md
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(repoRoot, "skills", "sampleskill", "SKILL.md")
}

func TestParseSkillFrontmatter_SampleSkillFile(t *testing.T) {
	t.Parallel()

	path := sampleSkillPath(t)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample skill at %s: %v", path, err)
	}

	fm, err := parseSkillFrontmatter(content)
	if err != nil {
		t.Fatalf("parseSkillFrontmatter: %v", err)
	}

	if fm.Name != "sampleskill" {
		t.Errorf("Name = %q, want %q", fm.Name, "sampleskill")
	}
	if fm.Description == "" {
		t.Error("Description is empty")
	}
	want := []string{"API_KEY", "PROVIDER_NAME"}
	if !reflect.DeepEqual(fm.RequiredEnvVars, want) {
		t.Errorf("RequiredEnvVars = %v, want %v", fm.RequiredEnvVars, want)
	}
}

func TestParseSkillFrontmatter_ErrorCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		content     string
		wantErrSubs string
	}{
		{
			name:        "missing opening ---",
			content:     "name: foo\ndescription: bar\n",
			wantErrSubs: "missing frontmatter opening ---",
		},
		{
			name:        "missing closing ---",
			content:     "---\nname: foo\ndescription: bar\n",
			wantErrSubs: "missing frontmatter closing ---",
		},
		{
			name:        "missing name",
			content:     "---\ndescription: bar\n---\nbody\n",
			wantErrSubs: "missing name",
		},
		{
			name:        "missing description",
			content:     "---\nname: foo\n---\nbody\n",
			wantErrSubs: "missing description",
		},
		{
			name:        "malformed YAML",
			content:     "---\nname: [unclosed\n---\nbody\n",
			wantErrSubs: "parse frontmatter YAML",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseSkillFrontmatter([]byte(tc.content))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubs)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSubs)
			}
		})
	}
}

func TestParseRequiredEnvVars_RoundTrip(t *testing.T) {
	t.Parallel()

	in := []string{"API_KEY", "PROVIDER_NAME"}
	encoded := encodeRequiredEnvVars(in)
	if encoded != `["API_KEY","PROVIDER_NAME"]` {
		t.Errorf("encoded = %q, want %q", encoded, `["API_KEY","PROVIDER_NAME"]`)
	}
	decoded := parseRequiredEnvVars(encoded)
	if !reflect.DeepEqual(decoded, in) {
		t.Errorf("decoded = %v, want %v", decoded, in)
	}
}

func TestParseRequiredEnvVars_EdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty string", "", []string{}},
		{"empty array literal", "[]", []string{}},
		{"invalid JSON", "not json", []string{}},
		{"JSON null", "null", []string{}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseRequiredEnvVars(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseRequiredEnvVars(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestEncodeRequiredEnvVars_Empty(t *testing.T) {
	t.Parallel()

	if got := encodeRequiredEnvVars(nil); got != "[]" {
		t.Errorf("encodeRequiredEnvVars(nil) = %q, want %q", got, "[]")
	}
	if got := encodeRequiredEnvVars([]string{}); got != "[]" {
		t.Errorf("encodeRequiredEnvVars([]) = %q, want %q", got, "[]")
	}
}
