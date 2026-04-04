package backup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// Paths excluded from backup archives.
var defaultExclusions = []string{
	"/proc", "/sys", "/dev", "/tmp", "/run",
	"/dev/shm", "/var/cache/apt", "/var/lib/apt/lists",
	"/var/log/journal",
}

// Path alias mappings.
var pathAliases = map[string]string{
	"HOME":     "/home/claworc",
	"Homebrew": "/home/linuxbrew/.linuxbrew",
}

// BackupDir returns the root directory for backup archives.
func BackupDir() string {
	return filepath.Join(config.Cfg.DataPath, "backups")
}

// ResolvePaths converts path aliases to absolute paths.
// HOME → /home/claworc, Homebrew → /home/linuxbrew/.linuxbrew, / → /, anything else is literal.
func ResolvePaths(aliases []string) []string {
	if len(aliases) == 0 {
		return []string{pathAliases["HOME"]}
	}
	resolved := make([]string, 0, len(aliases))
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if mapped, ok := pathAliases[a]; ok {
			resolved = append(resolved, mapped)
		} else {
			resolved = append(resolved, a)
		}
	}
	if len(resolved) == 0 {
		return []string{pathAliases["HOME"]}
	}
	return resolved
}

// CreateFullBackup creates a full backup of the specified paths in the given instance.
// It runs asynchronously — the backup is created in a goroutine.
func CreateFullBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName string, instanceID uint, note string, paths []string) (uint, error) {
	now := time.Now().UTC()
	dir := filepath.Join(BackupDir(), instanceName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("create backup dir: %w", err)
	}

	resolvedPaths := ResolvePaths(paths)
	pathsJSON, _ := json.Marshal(paths)

	b := &database.Backup{
		InstanceID:   instanceID,
		InstanceName: instanceName,
		Status:       "running",
		Paths:        string(pathsJSON),
		Note:         note,
	}
	if err := database.CreateBackup(b); err != nil {
		return 0, fmt.Errorf("create backup record: %w", err)
	}

	filename := fmt.Sprintf("%s-%d-%s.tar.gz", instanceName, b.ID, now.Format("20060102-150405"))
	relPath := filepath.Join(instanceName, filename)
	absPath := filepath.Join(BackupDir(), relPath)

	if err := database.UpdateBackup(b.ID, map[string]interface{}{"file_path": relPath}); err != nil {
		return b.ID, fmt.Errorf("update backup path: %w", err)
	}

	go func() {
		if err := runFullBackup(context.Background(), orch, instanceName, absPath, b.ID, resolvedPaths); err != nil {
			log.Printf("backup %d failed: %v", b.ID, err)
			finishBackup(b.ID, 0, err)
		}
	}()

	return b.ID, nil
}

func runFullBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName, absPath string, backupID uint, paths []string) error {
	f, err := os.Create(absPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	cmd := buildTarCommand(paths)
	stderr, exitCode, err := orch.StreamExecInInstance(ctx, instanceName, cmd, gw)
	if err != nil {
		return fmt.Errorf("stream exec: %w", err)
	}
	// tar may exit with code 1 for "file changed as we read it" — acceptable
	if exitCode > 1 {
		return fmt.Errorf("tar exited with code %d: %s", exitCode, stderr)
	}

	gw.Close()
	f.Close()

	stat, _ := os.Stat(absPath)
	size := int64(0)
	if stat != nil {
		size = stat.Size()
	}

	now := time.Now().UTC()
	return database.UpdateBackup(backupID, map[string]interface{}{
		"status":       "completed",
		"size_bytes":   size,
		"completed_at": &now,
	})
}

func buildTarCommand(paths []string) []string {
	excludes := make([]string, 0, len(defaultExclusions))
	for _, e := range defaultExclusions {
		excludes = append(excludes, "--exclude="+e)
	}
	args := append([]string{"tar", "-cf", "-"}, excludes...)
	args = append(args, paths...)
	return []string{"sh", "-c", strings.Join(args, " ") + " 2>/dev/null; exit 0"}
}

func finishBackup(backupID uint, size int64, backupErr error) {
	updates := map[string]interface{}{
		"status":     "failed",
		"size_bytes": size,
	}
	if backupErr != nil {
		updates["error_message"] = backupErr.Error()
	}
	now := time.Now().UTC()
	updates["completed_at"] = &now
	if err := database.UpdateBackup(backupID, updates); err != nil {
		log.Printf("failed to update backup %d status: %v", backupID, err)
	}
}
