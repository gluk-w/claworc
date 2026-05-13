package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00001_baseline: empty marker, kept only so goose's version table has a
// row at v1 on every install. Schema materialization used to live here,
// but the current policy is to run AutoMigrateAll on every boot from
// RunMigrations — see docs/migrations.md. The v1 row is preserved so
// existing installs (which already stamped this version) don't see a
// missing-migration warning.
func init() {
	register(&goose.Migration{
		Version: 1,
		Source:  "00001_baseline.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return fmt.Errorf("baseline migration is not reversible")
		},
	})
}

// AutoMigrateAll runs GORM AutoMigrate for every model the control plane
// owns. It is called unconditionally by RunMigrations on every boot so
// additive schema changes (new tables, new columns, new indexes) appear
// on both fresh installs and upgrades without a hand-written migration.
//
// This is the canonical list of models that participate in schema
// management; data-only migrations should reference it for completeness.
func AutoMigrateAll(gdb interface {
	AutoMigrate(dst ...interface{}) error
}) error {
	return gdb.AutoMigrate(
		&models.Instance{},
		&models.Setting{},
		&models.User{},
		&models.UserInstance{},
		&models.WebAuthnCredential{},
		&models.LLMProvider{},
		&models.LLMGatewayKey{},
		&models.Skill{},
		&models.Backup{},
		&models.BackupSchedule{},
		&models.SharedFolder{},
		&models.KanbanBoard{},
		&models.KanbanTask{},
		&models.KanbanComment{},
		&models.KanbanArtifact{},
		&models.InstanceSoul{},
		&models.BrowserSession{},
		&models.Team{},
		&models.TeamMember{},
		&models.TeamProvider{},
	)
}
