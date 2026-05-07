package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
)

// migrationFS embeds the SQL migration files. Files are versioned and named
// goose-style (e.g. 00002_add_users_email.sql). See docs/databases.md.
// The directory is intentionally kept empty in the initial commit — the
// baseline schema is materialized by AutoMigrate (registered as a Go
// migration below), and future schema deltas should land here as
// hand-written SQL.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// RunMigrations runs goose against the active main DB. It registers a Go
// baseline migration (v1) that re-uses GORM AutoMigrate to materialize the
// 17-model schema, then applies any SQL delta migrations embedded in
// migrations/. Idempotent: existing installs that already have the schema
// pass through the baseline as a no-op and get stamped at v1.
func RunMigrations(ctx context.Context) error {
	if DB == nil {
		return fmt.Errorf("RunMigrations called before Init")
	}
	if resolved == nil {
		return fmt.Errorf("RunMigrations called before Init (no resolved driver)")
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}

	dialect, err := gooseDialect(resolved.Driver)
	if err != nil {
		return err
	}
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}

	registerGoMigrations()

	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("locate embedded migrations: %w", err)
	}
	goose.SetBaseFS(sub)

	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

func gooseDialect(d Driver) (string, error) {
	switch d {
	case DriverSQLite:
		return "sqlite3", nil
	case DriverPostgres:
		return "postgres", nil
	case DriverMySQL:
		return "mysql", nil
	default:
		return "", fmt.Errorf("unsupported goose dialect: %s", d)
	}
}

var goMigrationsRegistered bool

// registerGoMigrations wires up Go-based migrations exactly once per process.
// goose's registry is global, so calling Add* twice panics.
func registerGoMigrations() {
	if goMigrationsRegistered {
		return
	}
	goMigrationsRegistered = true

	// 00001_baseline: materialize the schema via GORM AutoMigrate.
	//
	// AutoMigrate is idempotent and works across SQLite/Postgres/MySQL, so
	// for upgrades on existing SQLite installs this is effectively a no-op.
	// For fresh installs on any driver it creates every table, index, and
	// foreign key declared on the GORM models.
	goose.AddNamedMigrationContext("00001_baseline.go",
		func(ctx context.Context, tx *sql.Tx) error {
			// AutoMigrate needs a *gorm.DB; use the package global which is
			// already opened against the correct dialect. The migration runs
			// inside a goose-managed transaction, but AutoMigrate manages
			// its own DDL — tx is unused here intentionally.
			_ = tx
			return autoMigrateMain(DB)
		},
		func(ctx context.Context, tx *sql.Tx) error {
			// Down for the baseline is intentionally not implemented:
			// dropping every table is destructive and out of scope.
			return fmt.Errorf("baseline migration is not reversible")
		},
	)

	// 00004_seed_teams: data backfill on top of 00003_create_teams.sql.
	// Ensures the seeded "Default" team exists, backfills any instance
	// that came in with team_id=0, mirrors existing UserInstance grants
	// into TeamMember(role=user), and promotes users whose legacy
	// can_create_instances flag was set to manager of the Default team.
	// Safe to re-run.
	goose.AddNamedMigrationContext("00004_seed_teams.go",
		func(ctx context.Context, tx *sql.Tx) error {
			_ = tx
			return seedTeamsAndBackfill(DB)
		},
		func(ctx context.Context, tx *sql.Tx) error {
			return fmt.Errorf("seed_teams migration is not reversible")
		},
	)
}

// seedTeamsAndBackfill creates a "Default Team" only when the teams
// table is empty, backfills any instance with team_id=0 to point at the
// first team, mirrors UserInstance rows into TeamMember(role=user), and
// promotes users with can_create_instances=true to manager of that team.
// Idempotent.
//
// The teams/team_members/team_providers tables are created by the
// sibling SQL migration 00003_create_teams.sql. The instances.team_id
// column is added here via AutoMigrate so it's idempotent: on fresh
// installs the baseline already created it from the current Instance
// model (no-op); on upgrades the baseline is stamped, leaving this
// AutoMigrate call to add the column.
func seedTeamsAndBackfill(gdb *gorm.DB) error {
	if err := gdb.AutoMigrate(&Instance{}); err != nil {
		return fmt.Errorf("auto-migrate instances.team_id: %w", err)
	}

	var teamCount int64
	if err := gdb.Model(&Team{}).Count(&teamCount).Error; err != nil {
		return fmt.Errorf("count teams: %w", err)
	}
	var defaultTeam Team
	if teamCount == 0 {
		defaultTeam = Team{Name: "Default Team", Description: "Default team"}
		if err := gdb.Create(&defaultTeam).Error; err != nil {
			return fmt.Errorf("seed default team: %w", err)
		}
	} else {
		if err := gdb.Order("id asc").First(&defaultTeam).Error; err != nil {
			return fmt.Errorf("load anchor team: %w", err)
		}
	}

	if err := gdb.Model(&Instance{}).Where("team_id IS NULL OR team_id = 0").
		Update("team_id", defaultTeam.ID).Error; err != nil {
		return fmt.Errorf("backfill instance.team_id: %w", err)
	}

	var grants []UserInstance
	if err := gdb.Find(&grants).Error; err != nil {
		return fmt.Errorf("load user_instances: %w", err)
	}
	for _, g := range grants {
		var inst Instance
		if err := gdb.First(&inst, g.InstanceID).Error; err != nil {
			continue
		}
		teamID := inst.TeamID
		if teamID == 0 {
			teamID = defaultTeam.ID
		}
		var existing int64
		gdb.Model(&TeamMember{}).Where("team_id = ? AND user_id = ?", teamID, g.UserID).Count(&existing)
		if existing == 0 {
			gdb.Create(&TeamMember{TeamID: teamID, UserID: g.UserID, Role: "user"})
		}
	}

	var creators []User
	if err := gdb.Where("can_create_instances = ?", true).Find(&creators).Error; err != nil {
		return fmt.Errorf("load creators: %w", err)
	}
	for _, u := range creators {
		var existing TeamMember
		err := gdb.Where("team_id = ? AND user_id = ?", defaultTeam.ID, u.ID).First(&existing).Error
		if err != nil {
			gdb.Create(&TeamMember{TeamID: defaultTeam.ID, UserID: u.ID, Role: "manager"})
		} else if existing.Role != "manager" {
			gdb.Model(&existing).Update("role", "manager")
		}
	}

	return nil
}

// resetGoMigrationsForTest is exposed for tests so multiple Init/Migrate
// cycles in a single process don't trigger the "register once" guard.
func resetGoMigrationsForTest() {
	goMigrationsRegistered = false
	goose.ResetGlobalMigrations()
}

// Compile-time guard: ensure DB satisfies the gorm.DB shape we use.
var _ = (*gorm.DB)(nil)
