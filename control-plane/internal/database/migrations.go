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
}

// resetGoMigrationsForTest is exposed for tests so multiple Init/Migrate
// cycles in a single process don't trigger the "register once" guard.
func resetGoMigrationsForTest() {
	goMigrationsRegistered = false
	goose.ResetGlobalMigrations()
}

// Compile-time guard: ensure DB satisfies the gorm.DB shape we use.
var _ = (*gorm.DB)(nil)
