package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00007_backfill_instance_uuid: populates Instance.UUID for rows that
// pre-date the column. AutoMigrateAll creates the column (and its unique
// index) on every boot; this migration just walks rows where uuid IS
// NULL OR '' and assigns a fresh v4 UUID. Idempotent.
func init() {
	register(&goose.Migration{
		Version: 7,
		Source:  "00007_backfill_instance_uuid.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			_ = tx
			return backfillInstanceUUID(DB())
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return fmt.Errorf("backfill_instance_uuid migration is not reversible")
		},
	})
}

func backfillInstanceUUID(gdb *gorm.DB) error {
	var insts []models.Instance
	if err := gdb.Where("uuid IS NULL OR uuid = ''").Find(&insts).Error; err != nil {
		return fmt.Errorf("load instances missing uuid: %w", err)
	}
	for _, inst := range insts {
		newUUID := uuid.New().String()
		if err := gdb.Model(&models.Instance{}).Where("id = ?", inst.ID).
			Update("uuid", newUUID).Error; err != nil {
			return fmt.Errorf("backfill uuid for instance %d: %w", inst.ID, err)
		}
	}
	return nil
}
