package database

import (
	"context"
	"fmt"

	"github.com/pressly/goose/v3"

	"github.com/gluk-w/claworc/control-plane/internal/database/migrations"
)

// RunMigrations applies the full migration set against the active main
// DB. It hands the live *gorm.DB and dialect string to the migrations
// subpackage (so Go migrations can drive AutoMigrate and the GORM
// Migrator interface), then merges the subpackage's Go migrations with
// the SQL files embedded in internal/database/migrations into goose's
// global registry and runs goose Up.
//
// Idempotent: already-applied migrations are skipped by goose, and the
// global registry is reset on every call so the function is safe to
// invoke multiple times in the same process (used by tests that drive
// the full Init() path).
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

	// Wire the live DB into the migrations subpackage so Go migrations
	// can call DB() and WithMigrator at apply time.
	migrations.Configure(DB, string(resolved.Driver))

	// Run AutoMigrate against every model on every boot. Additive schema
	// changes (new tables, new columns, new indexes) land for free on
	// fresh installs and upgrades, so the only migrations that need to be
	// hand-written are data backfills and the few schema changes
	// AutoMigrate can't express (type changes, drops, renames). See
	// docs/migrations.md for the full policy.
	if err := migrations.AutoMigrateAll(DB); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	// Reset the goose global registry and re-register every Go migration
	// declared in the subpackage. Resetting is necessary because
	// AddNamedMigrationContext panics on duplicates — and any single
	// process may call RunMigrations more than once (notably in tests).
	goose.ResetGlobalMigrations()
	for _, m := range migrations.All() {
		goose.AddNamedMigrationContext(m.Source, m.UpFnContext, m.DownFnContext)
	}

	// All migrations are Go; no embedded .sql files remain. Pass nil to
	// SetBaseFS so goose's default os.DirFS is used; combined with the
	// "." directory argument below, goose finds no .sql files (the
	// control-plane binary's working directory has none) and applies
	// only the Go migrations registered above.
	goose.SetBaseFS(nil)

	// WithAllowMissing tolerates the case where an older version is in the
	// registry but not yet applied to the DB while a newer version *is*
	// applied — which happens for any dev DB that ran an intermediate
	// state of this branch where v2 was removed from the registry and v3+
	// were stamped. On main installs (v1+v2 stamped) and fresh installs
	// the option is a no-op.
	if err := goose.UpContext(ctx, sqlDB, ".", goose.WithAllowMissing()); err != nil {
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

// resetGoMigrationsForTest is retained as a no-op for test code that
// used to clear in-package registration state. RunMigrations now resets
// goose's global registry on every call, so no cleanup is required.
func resetGoMigrationsForTest() {}
