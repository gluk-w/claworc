package database

import (
	"path/filepath"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/config"
)

// TestInit_FreshSQLite drives the full Init() path (URL parsing, openDialector,
// goose migrations including the AutoMigrate baseline, default seeding) against
// a fresh SQLite file in a temp dir. This is the closest unit test we have to
// the production startup sequence.
func TestInit_FreshSQLite(t *testing.T) {
	dir := t.TempDir()
	prevDataPath := config.Cfg.DataPath
	prevDatabase := config.Cfg.Database
	config.Cfg.DataPath = dir
	config.Cfg.Database = "" // fall through to default SQLite
	t.Cleanup(func() {
		config.Cfg.DataPath = prevDataPath
		config.Cfg.Database = prevDatabase
		if DB != nil {
			Close()
			DB = nil
		}
		LogsDB = nil
		resolved = nil
		resetGoMigrationsForTest()
	})

	resetGoMigrationsForTest()
	if err := Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if DB == nil {
		t.Fatal("DB is nil after Init")
	}

	// Default seed should be present.
	val, err := GetSetting("default_cpu_request")
	if err != nil {
		t.Fatalf("GetSetting default_cpu_request: %v", err)
	}
	if val != "500m" {
		t.Errorf("default_cpu_request = %q, want 500m", val)
	}

	// Logs DB initialization for SQLite mode opens a sibling file.
	if err := InitLogsDB(dir); err != nil {
		t.Fatalf("InitLogsDB: %v", err)
	}
	if LogsDB == nil {
		t.Fatal("LogsDB is nil after InitLogsDB")
	}
	if LogsDB == DB {
		t.Error("expected separate LogsDB connection for SQLite mode")
	}

	// Sanity: the goose version table exists. Querying it through GORM is
	// driver-independent enough for a smoke check.
	type versionRow struct{ VersionID int64 }
	var rows []versionRow
	if err := DB.Raw("SELECT version_id FROM goose_db_version ORDER BY version_id").Scan(&rows).Error; err != nil {
		t.Fatalf("query goose_db_version: %v", err)
	}
	if len(rows) == 0 {
		t.Error("expected at least one applied goose migration")
	}

	_ = filepath.Join // keep import used in case of future tweaks
}
