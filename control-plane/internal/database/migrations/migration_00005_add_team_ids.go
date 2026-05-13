package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00005_add_team_ids: add the JSON-encoded TeamIDs column to
// shared_folders and backup_schedules so both can be scoped to entire
// teams.
//
// Needed on upgrade from earlier deployments (e.g. main) where the v1
// baseline was stamped against a model set that didn't yet declare these
// fields — goose skips the now-updated v1 body, so the columns must be
// added by a delta. Fresh installs land via v1's AutoMigrate against the
// current model set, which already creates the columns; the HasColumn
// guards below turn this migration into a no-op in that case.
func init() {
	register(&goose.Migration{
		Version: 5,
		Source:  "00005_add_team_ids.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if !m.HasColumn(&models.SharedFolder{}, "TeamIDs") {
					if err := m.AddColumn(&models.SharedFolder{}, "TeamIDs"); err != nil {
						return err
					}
				}
				if !m.HasColumn(&models.BackupSchedule{}, "TeamIDs") {
					if err := m.AddColumn(&models.BackupSchedule{}, "TeamIDs"); err != nil {
						return err
					}
				}
				return nil
			})
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if m.HasColumn(&models.BackupSchedule{}, "TeamIDs") {
					if err := m.DropColumn(&models.BackupSchedule{}, "TeamIDs"); err != nil {
						return err
					}
				}
				if m.HasColumn(&models.SharedFolder{}, "TeamIDs") {
					if err := m.DropColumn(&models.SharedFolder{}, "TeamIDs"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	})
}
