package backup

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// RestoreBackup restores a backup to the given instance.
// The instance must be running. This runs synchronously.
func RestoreBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName string, backupID uint) error {
	b, err := database.GetBackup(backupID)
	if err != nil {
		return fmt.Errorf("get backup %d: %w", backupID, err)
	}

	if b.Status != "completed" {
		return fmt.Errorf("backup %d has status %q, expected completed", b.ID, b.Status)
	}

	absPath := filepath.Join(BackupDir(), b.FilePath)
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("backup file missing for backup %d: %w", b.ID, err)
	}

	log.Printf("restoring backup %d to instance %s", b.ID, instanceName)
	if err := restoreArchive(ctx, orch, instanceName, absPath); err != nil {
		return fmt.Errorf("restore backup %d: %w", b.ID, err)
	}

	return nil
}

// restoreArchive extracts a tar.gz archive into the container's root filesystem
// by streaming it in base64-encoded chunks via ExecInInstance.
func restoreArchive(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName, archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	tmpPath := "/tmp/_claworc_restore.tar.gz"

	// Clean up any leftover temp file
	orch.ExecInInstance(ctx, instanceName, []string{"sh", "-c", "rm -f " + tmpPath})

	// Stream in 48KB chunks (base64 encoded, safe for shell)
	const chunkSize = 48 * 1024
	buf := make([]byte, chunkSize)

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			encoded := base64.StdEncoding.EncodeToString(buf[:n])
			cmd := fmt.Sprintf("echo '%s' | base64 -d >> %s", encoded, tmpPath)
			_, stderr, exitCode, err := orch.ExecInInstance(ctx, instanceName, []string{"sh", "-c", cmd})
			if err != nil {
				return fmt.Errorf("write chunk: %w", err)
			}
			if exitCode != 0 {
				return fmt.Errorf("write chunk failed (exit %d): %s", exitCode, stderr)
			}
		}
		if readErr != nil {
			break
		}
	}

	// Extract and clean up
	_, stderr, exitCode, err := orch.ExecInInstance(ctx, instanceName,
		[]string{"sh", "-c", fmt.Sprintf("tar xzf %s -C / 2>/dev/null; rm -f %s; exit 0", tmpPath, tmpPath)})
	if err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}
	if exitCode > 1 {
		return fmt.Errorf("extract failed (exit %d): %s", exitCode, stderr)
	}

	return nil
}
