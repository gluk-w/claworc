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

// GetBackupChain resolves the full chain of backups needed to restore a given backup.
// Returns [full, incr1, incr2, ...] in the order they should be applied.
func GetBackupChain(backupID uint) ([]database.Backup, error) {
	var chain []database.Backup
	current, err := database.GetBackup(backupID)
	if err != nil {
		return nil, fmt.Errorf("get backup %d: %w", backupID, err)
	}

	chain = append(chain, *current)
	for current.ParentID != nil {
		parentID := *current.ParentID
		current, err = database.GetBackup(parentID)
		if err != nil {
			return nil, fmt.Errorf("get parent backup %d: %w", parentID, err)
		}
		chain = append([]database.Backup{*current}, chain...)
	}

	if chain[0].Type != "full" {
		return nil, fmt.Errorf("backup chain root (id=%d) is not a full backup", chain[0].ID)
	}

	return chain, nil
}

// RestoreBackup restores a backup (and its chain) to the given instance.
// The instance must be running. This runs synchronously.
func RestoreBackup(ctx context.Context, orch orchestrator.ContainerOrchestrator, instanceName string, backupID uint) error {
	chain, err := GetBackupChain(backupID)
	if err != nil {
		return fmt.Errorf("resolve backup chain: %w", err)
	}

	for _, b := range chain {
		if b.Status != "completed" {
			return fmt.Errorf("backup %d in chain has status %q, expected completed", b.ID, b.Status)
		}
		absPath := filepath.Join(BackupDir(), b.FilePath)
		if _, err := os.Stat(absPath); err != nil {
			return fmt.Errorf("backup file missing for backup %d: %w", b.ID, err)
		}
	}

	for i, b := range chain {
		log.Printf("restoring backup %d (%s, %d/%d) to instance %s", b.ID, b.Type, i+1, len(chain), instanceName)
		absPath := filepath.Join(BackupDir(), b.FilePath)
		if err := restoreArchive(ctx, orch, instanceName, absPath); err != nil {
			return fmt.Errorf("restore backup %d: %w", b.ID, err)
		}
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
