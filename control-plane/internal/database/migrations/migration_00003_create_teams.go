package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00003_create_teams: create the teams / team_members / team_providers
// tables that back the teams feature. Counterpart of the original
// 00003_create_teams.sql migration, rewritten in Go using the GORM
// Migrator interface so the schema is driver-portable.
//
// Idempotent on two axes:
//
//   - For deployments that already applied the prior SQL version of 00003,
//     goose skips this migration entirely (the goose_db_version row at
//     version 3 marks it as done — goose tracks by version number, not by
//     the Source string).
//   - For fresh installs, the 00001 baseline AutoMigrate already created
//     the same tables from the GORM models, so this migration uses
//     HasTable / HasIndex guards and no-ops in that case.
//
// The companion column instances.team_id is added by the 00004 seed
// migration via AutoMigrate, which is itself idempotent on every driver.
func init() {
	register(&goose.Migration{
		Version: 3,
		Source:  "00003_create_teams.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if !m.HasTable(&models.Team{}) {
					if err := m.CreateTable(&models.Team{}); err != nil {
						return err
					}
				}
				// The Team model declares `uniqueIndex` on Name, so GORM
				// names the index idx_teams_name — same as the SQL did.
				if !m.HasIndex(&models.Team{}, "Name") {
					if err := m.CreateIndex(&models.Team{}, "Name"); err != nil {
						return err
					}
				}
				if !m.HasTable(&models.TeamMember{}) {
					if err := m.CreateTable(&models.TeamMember{}); err != nil {
						return err
					}
				}
				if !m.HasTable(&models.TeamProvider{}) {
					if err := m.CreateTable(&models.TeamProvider{}); err != nil {
						return err
					}
				}
				return nil
			})
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if m.HasTable(&models.TeamProvider{}) {
					if err := m.DropTable(&models.TeamProvider{}); err != nil {
						return err
					}
				}
				if m.HasTable(&models.TeamMember{}) {
					if err := m.DropTable(&models.TeamMember{}); err != nil {
						return err
					}
				}
				if m.HasTable(&models.Team{}) {
					if err := m.DropTable(&models.Team{}); err != nil {
						return err
					}
				}
				return nil
			})
		},
	})
}
