package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/config"
)

func TestValidateHostMountPath(t *testing.T) {
	// Real directory used to exercise the EvalSymlinks branch.
	tmp := t.TempDir()
	allowedDir := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(filepath.Join(allowedDir, "obsidian"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// A symlink inside the allowed dir pointing outside of it.
	escape := filepath.Join(allowedDir, "escape")
	if err := os.Symlink("/etc", escape); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Pretend the Claworc data dir lives under the allowlist to prove the
	// defense-in-depth denylist still wins.
	dataDir := filepath.Join(allowedDir, "claworc-data")
	prevData := config.Cfg.DataPath
	config.Cfg.DataPath = dataDir
	t.Cleanup(func() { config.Cfg.DataPath = prevData })

	allowed := []string{allowedDir}

	tests := []struct {
		name     string
		hostPath string
		allowed  []string
		wantErr  bool
	}{
		{"feature disabled", filepath.Join(allowedDir, "obsidian"), nil, true},
		{"feature disabled blank prefixes", filepath.Join(allowedDir, "obsidian"), []string{"", "  "}, true},
		{"relative path", "relative/path", allowed, true},
		{"parent traversal", allowedDir + "/../etc", allowed, true},
		{"unclean trailing slash", filepath.Join(allowedDir, "obsidian") + "/", allowed, true},
		{"outside allowlist", "/var/tmp/x", allowed, true},
		{"docker socket", "/var/run/docker.sock", []string{"/var/run"}, true},
		{"claworc data dir", dataDir, allowed, true},
		{"claworc data subdir", filepath.Join(dataDir, "keys"), allowed, true},
		{"symlink escape", escape, allowed, true},
		{"exact prefix match", allowedDir, allowed, false},
		{"valid subdir", filepath.Join(allowedDir, "obsidian"), allowed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHostMountPath(tt.hostPath, tt.allowed)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %q, got nil", tt.hostPath)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error for %q, got %v", tt.hostPath, err)
			}
		})
	}
}
