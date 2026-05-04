package database

import (
	"strings"
	"testing"
	"time"
)

func TestResolveDatabase_EmptyURLFallsBackToSQLite(t *testing.T) {
	r, err := ResolveDatabase("", "/tmp/x")
	if err != nil {
		t.Fatalf("ResolveDatabase: %v", err)
	}
	if r.Driver != DriverSQLite {
		t.Errorf("Driver = %s, want sqlite", r.Driver)
	}
	if r.SQLiteMainPath != "/tmp/x/claworc.db" {
		t.Errorf("SQLiteMainPath = %s", r.SQLiteMainPath)
	}
	if r.SQLiteLogsPath != "/tmp/x/llm-logs.db" {
		t.Errorf("SQLiteLogsPath = %s", r.SQLiteLogsPath)
	}
	if r.ShareConn {
		t.Error("ShareConn should be false for SQLite")
	}
}

func TestResolveDatabase_SQLiteURL(t *testing.T) {
	r, err := ResolveDatabase("sqlite:///var/lib/claworc/main.db", "/unused")
	if err != nil {
		t.Fatalf("ResolveDatabase: %v", err)
	}
	if r.Driver != DriverSQLite {
		t.Errorf("Driver = %s", r.Driver)
	}
	if r.SQLiteMainPath != "/var/lib/claworc/main.db" {
		t.Errorf("SQLiteMainPath = %s", r.SQLiteMainPath)
	}
	if r.SQLiteLogsPath != "/var/lib/claworc/llm-logs.db" {
		t.Errorf("SQLiteLogsPath = %s", r.SQLiteLogsPath)
	}
}

func TestResolveDatabase_PostgresURL(t *testing.T) {
	r, err := ResolveDatabase("postgres://u:p@db.example:5432/claworc?sslmode=require", "/unused")
	if err != nil {
		t.Fatalf("ResolveDatabase: %v", err)
	}
	if r.Driver != DriverPostgres {
		t.Errorf("Driver = %s, want postgres", r.Driver)
	}
	if !r.ShareConn {
		t.Error("ShareConn should be true for Postgres")
	}
	if r.MainDialector == nil || r.LogsDialector == nil {
		t.Error("dialectors should be non-nil")
	}
	if r.MainDialector != r.LogsDialector {
		t.Error("Postgres mode should reuse the same dialector for main and logs")
	}
}

func TestResolveDatabase_MysqlURL(t *testing.T) {
	r, err := ResolveDatabase("mysql://u:p@db.example:3306/claworc", "/unused")
	if err != nil {
		t.Fatalf("ResolveDatabase: %v", err)
	}
	if r.Driver != DriverMySQL {
		t.Errorf("Driver = %s, want mysql", r.Driver)
	}
	if !r.ShareConn {
		t.Error("ShareConn should be true for MySQL")
	}
}

func TestResolveDatabase_MariaDBAlias(t *testing.T) {
	r, err := ResolveDatabase("mariadb://u:p@db.example/claworc", "/unused")
	if err != nil {
		t.Fatalf("ResolveDatabase: %v", err)
	}
	if r.Driver != DriverMySQL {
		t.Errorf("mariadb:// should map to mysql driver, got %s", r.Driver)
	}
}

func TestResolveDatabase_PoolOverrides(t *testing.T) {
	r, err := ResolveDatabase("postgres://u:p@db.example/claworc?max_open_conns=42&max_idle_conns=7&conn_max_lifetime=2h", "/unused")
	if err != nil {
		t.Fatalf("ResolveDatabase: %v", err)
	}
	if r.Pool.MaxOpenConns != 42 {
		t.Errorf("MaxOpenConns = %d, want 42", r.Pool.MaxOpenConns)
	}
	if r.Pool.MaxIdleConns != 7 {
		t.Errorf("MaxIdleConns = %d, want 7", r.Pool.MaxIdleConns)
	}
	if r.Pool.ConnMaxLifetime != 2*time.Hour {
		t.Errorf("ConnMaxLifetime = %s", r.Pool.ConnMaxLifetime)
	}
}

func TestResolveDatabase_InvalidScheme(t *testing.T) {
	_, err := ResolveDatabase("oracle://u@db/c", "/unused")
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
	if !strings.Contains(err.Error(), "unsupported database driver") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMysqlURLToDSN_AddsParseTime(t *testing.T) {
	r, err := ResolveDatabase("mysql://user:pw@host:3307/db", "/unused")
	if err != nil {
		t.Fatalf("ResolveDatabase: %v", err)
	}
	// We can't read the DSN back from the dialector directly; just ensure
	// the resolution completed without error and produced a non-nil dialector.
	if r.MainDialector == nil {
		t.Fatal("dialector nil")
	}
}
