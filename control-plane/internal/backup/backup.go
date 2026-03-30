package backup

import (
	"compress/gzip"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
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

// BackupDir returns the root directory for backup archives.
func BackupDir() string {
	return filepath.Join(config.Cfg.DataPath, "backups")
}

// CreateFullBackup creates a full rootfs backup of the given instance.
// It runs asynchronously — the caller should invoke this in a goroutine.
func CreateFullBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName string, instanceID uint, note string) (uint, error) {
	now := time.Now().UTC()
	dir := filepath.Join(BackupDir(), instanceName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("create backup dir: %w", err)
	}

	b := &database.Backup{
		InstanceID:   instanceID,
		InstanceName: instanceName,
		Type:         "full",
		Status:       "running",
		FilePath:     "", // set after ID is known
		Note:         note,
	}
	if err := database.CreateBackup(b); err != nil {
		return 0, fmt.Errorf("create backup record: %w", err)
	}

	filename := fmt.Sprintf("%d-full-%s.tar.gz", b.ID, now.Format("20060102-150405"))
	relPath := filepath.Join(instanceName, filename)
	absPath := filepath.Join(BackupDir(), relPath)

	if err := database.UpdateBackup(b.ID, map[string]interface{}{"file_path": relPath}); err != nil {
		return b.ID, fmt.Errorf("update backup path: %w", err)
	}

	go func() {
		if err := runFullBackup(ctx, orch, instanceName, absPath, b.ID); err != nil {
			log.Printf("backup %d failed: %v", b.ID, err)
			finishBackup(b.ID, 0, err)
		}
	}()

	return b.ID, nil
}

// CreateIncrementalBackup creates an incremental backup based on the latest completed backup.
func CreateIncrementalBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName string, instanceID uint, note string) (uint, error) {
	parent, err := database.GetLatestCompletedBackup(instanceID)
	if err != nil {
		// No previous backup — fall back to full
		return CreateFullBackup(ctx, orch, instanceName, instanceID, note)
	}

	now := time.Now().UTC()
	dir := filepath.Join(BackupDir(), instanceName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("create backup dir: %w", err)
	}

	b := &database.Backup{
		InstanceID:   instanceID,
		InstanceName: instanceName,
		Type:         "incremental",
		ParentID:     &parent.ID,
		Status:       "running",
		Note:         note,
	}
	if err := database.CreateBackup(b); err != nil {
		return 0, fmt.Errorf("create backup record: %w", err)
	}

	filename := fmt.Sprintf("%d-incr-%s.tar.gz", b.ID, now.Format("20060102-150405"))
	relPath := filepath.Join(instanceName, filename)
	absPath := filepath.Join(BackupDir(), relPath)

	if err := database.UpdateBackup(b.ID, map[string]interface{}{"file_path": relPath}); err != nil {
		return b.ID, fmt.Errorf("update backup path: %w", err)
	}

	go func() {
		if err := runIncrementalBackup(ctx, orch, instanceName, absPath, b.ID, parent.MarkerTime); err != nil {
			log.Printf("backup %d failed: %v", b.ID, err)
			finishBackup(b.ID, 0, err)
		}
	}()

	return b.ID, nil
}

func runFullBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName, absPath string, backupID uint) error {
	// Get the current container time before backup starts (for incremental baseline).
	markerEpoch, err := getContainerTime(ctx, orch, instanceName)
	if err != nil {
		return fmt.Errorf("get container time: %w", err)
	}

	f, err := os.Create(absPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	cmd := buildTarCommand()
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

	markerTime := time.Unix(markerEpoch, 0).UTC()
	now := time.Now().UTC()
	return database.UpdateBackup(backupID, map[string]interface{}{
		"status":       "completed",
		"size_bytes":   size,
		"marker_time":  markerTime,
		"completed_at": &now,
	})
}

func runIncrementalBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName, absPath string, backupID uint, since time.Time) error {
	markerEpoch, err := getContainerTime(ctx, orch, instanceName)
	if err != nil {
		return fmt.Errorf("get container time: %w", err)
	}

	f, err := os.Create(absPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	cmd := buildIncrementalCommand(since)
	stderr, exitCode, err := orch.StreamExecInInstance(ctx, instanceName, cmd, gw)
	if err != nil {
		return fmt.Errorf("stream exec: %w", err)
	}
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

	markerTime := time.Unix(markerEpoch, 0).UTC()
	now := time.Now().UTC()
	return database.UpdateBackup(backupID, map[string]interface{}{
		"status":       "completed",
		"size_bytes":   size,
		"marker_time":  markerTime,
		"completed_at": &now,
	})
}

func buildTarCommand() []string {
	excludes := make([]string, 0, len(defaultExclusions))
	for _, e := range defaultExclusions {
		excludes = append(excludes, "--exclude="+e)
	}
	args := append([]string{"tar", "-cf", "-"}, excludes...)
	args = append(args, "/")
	return []string{"sh", "-c", strings.Join(args, " ") + " 2>/dev/null; exit 0"}
}

func buildIncrementalCommand(since time.Time) []string {
	epoch := since.Unix()
	excludes := make([]string, 0, len(defaultExclusions)*2)
	for _, e := range defaultExclusions {
		excludes = append(excludes, "-not", "-path", e+"/*")
	}
	findExcludes := strings.Join(excludes, " ")

	// find changed files, pipe to tar
	cmd := fmt.Sprintf(
		"find / %s -newermt @%d -print0 2>/dev/null | tar -cf - --null -T - 2>/dev/null; exit 0",
		findExcludes, epoch,
	)
	return []string{"sh", "-c", cmd}
}

func getContainerTime(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName string) (int64, error) {
	stdout, _, exitCode, err := orch.ExecInInstance(ctx, instanceName, []string{"date", "+%s"})
	if err != nil {
		return 0, err
	}
	if exitCode != 0 {
		return 0, fmt.Errorf("date command exited with code %d", exitCode)
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(stdout), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse epoch %q: %w", stdout, err)
	}
	return epoch, nil
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
