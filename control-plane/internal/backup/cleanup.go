package backup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// DeleteBackup removes a backup file and its database record.
func DeleteBackup(backupID uint) error {
	b, err := database.GetBackup(backupID)
	if err != nil {
		return fmt.Errorf("get backup: %w", err)
	}

	// Remove the file
	if b.FilePath != "" {
		absPath := filepath.Join(BackupDir(), b.FilePath)
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove file: %w", err)
		}
	}

	// Remove DB record
	if err := database.DeleteBackupRecord(b.ID); err != nil {
		return fmt.Errorf("delete record: %w", err)
	}

	return nil
}
