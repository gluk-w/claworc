package moderator

import (
	"sort"
	"testing"
)

func TestExtractMentionedPaths_BacktickPaths(t *testing.T) {
	t.Parallel()
	workspace := "/home/claworc/.openclaw/workspace"
	transcript := "I created the file `report.py` and also saved `/home/claworc/.openclaw/workspace/data/output.csv`."

	got := ExtractMentionedPaths(transcript, workspace)
	sort.Strings(got)

	want := []string{"data/output.csv", "report.py"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractMentionedPaths_VerbPhrases(t *testing.T) {
	t.Parallel()
	workspace := "/home/claworc/.openclaw/workspace"
	transcript := "I saved to results.json and wrote analysis.md and created /home/claworc/.openclaw/workspace/out/chart.png"

	got := ExtractMentionedPaths(transcript, workspace)
	sort.Strings(got)

	want := []string{"analysis.md", "out/chart.png", "results.json"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractMentionedPaths_BareAbsolutePaths(t *testing.T) {
	t.Parallel()
	workspace := "/home/claworc/.openclaw/workspace"
	transcript := "The output is at /home/claworc/.openclaw/workspace/foo/bar.txt and /tmp/secret.log"

	got := ExtractMentionedPaths(transcript, workspace)

	if len(got) != 1 || got[0] != "foo/bar.txt" {
		t.Errorf("got %v, want [foo/bar.txt]", got)
	}
}

func TestExtractMentionedPaths_IgnoresOutsideWorkspace(t *testing.T) {
	t.Parallel()
	workspace := "/home/claworc/.openclaw/workspace"
	transcript := "I read /etc/passwd and /usr/bin/something.sh"

	got := ExtractMentionedPaths(transcript, workspace)
	if len(got) != 0 {
		t.Errorf("expected no results for paths outside workspace, got %v", got)
	}
}

func TestExtractMentionedPaths_Deduplication(t *testing.T) {
	t.Parallel()
	workspace := "/home/claworc/.openclaw/workspace"
	// Use backtick and bare path forms that resolve to the same relative path.
	transcript := "I created `/home/claworc/.openclaw/workspace/output.txt` then referenced /home/claworc/.openclaw/workspace/output.txt again"

	got := ExtractMentionedPaths(transcript, workspace)
	if len(got) != 1 {
		t.Errorf("expected 1 deduplicated result, got %v", got)
	}
	if len(got) > 0 && got[0] != "output.txt" {
		t.Errorf("got %q, want output.txt", got[0])
	}
}

func TestExtractMentionedPaths_EmptyTranscript(t *testing.T) {
	t.Parallel()
	got := ExtractMentionedPaths("", "/workspace")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestExtractMentionedPaths_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	workspace := "/home/claworc/.openclaw/workspace"
	transcript := "I saved `../../etc/passwd.txt` to disk."

	got := ExtractMentionedPaths(transcript, workspace)
	if len(got) != 0 {
		t.Errorf("expected path traversal to be rejected, got %v", got)
	}
}

func TestExtractMentionedPaths_NoExtension(t *testing.T) {
	t.Parallel()
	workspace := "/workspace"
	transcript := "I created `Makefile` and `README`"

	got := ExtractMentionedPaths(transcript, workspace)
	if len(got) != 0 {
		t.Errorf("expected no matches for extension-less files, got %v", got)
	}
}

func TestIsPathLike(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  bool
	}{
		{"report.py", true},
		{"data/output.csv", true},
		{"/absolute/path.txt", true},
		{"noext", false},
		{"", false},
		{"has spaces.txt", false},
		{"file.a", true},
		{"file.toolongextension123", false},
	}
	for _, tt := range tests {
		if got := isPathLike(tt.input); got != tt.want {
			t.Errorf("isPathLike(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestRelativizeToWorkspace(t *testing.T) {
	t.Parallel()
	ws := "/home/claworc/.openclaw/workspace"
	tests := []struct {
		raw     string
		wantRel string
		wantOK  bool
	}{
		{ws + "/foo.py", "foo.py", true},
		{ws + "/sub/dir/file.txt", "sub/dir/file.txt", true},
		{"/etc/passwd", "", false},              // absolute outside ws
		{"relative.py", "relative.py", true},    // relative path accepted
		{"../escape.py", "", false},             // path traversal rejected
		{ws + "/../etc/passwd", "", false},      // traversal via ws prefix
		{ws + "/", "", false},                   // empty relative
	}
	for _, tt := range tests {
		rel, ok := relativizeToWorkspace(tt.raw, ws)
		if ok != tt.wantOK {
			t.Errorf("relativizeToWorkspace(%q): ok=%v, want %v", tt.raw, ok, tt.wantOK)
		}
		if rel != tt.wantRel {
			t.Errorf("relativizeToWorkspace(%q): rel=%q, want %q", tt.raw, rel, tt.wantRel)
		}
	}
}
