package database

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Driver identifies a supported database backend.
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
	DriverMySQL    Driver = "mysql"
)

// PoolConfig captures connection pool tuning that applies to all drivers
// except SQLite (where the file-backed connection is effectively single-pool).
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DefaultPoolConfig returns sensible defaults for server-class drivers.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    20,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
	}
}

// ResolvedDB describes the parsed CLAWORC_DATABASE configuration.
//   - For SQLite, MainDialector and LogsDialector point to two distinct files
//     so the existing two-DB layout is preserved across upgrades.
//   - For Postgres / MySQL, MainDialector and LogsDialector are the same
//     dialector value; callers must open a single connection and reuse it for
//     both DB and LogsDB so all tables (including llm_request_logs) live in
//     the same database.
type ResolvedDB struct {
	Driver         Driver
	MainDialector  gorm.Dialector
	LogsDialector  gorm.Dialector
	ShareConn      bool // true when MainDialector and LogsDialector are the same DB
	Pool           PoolConfig
	SQLiteMainPath string // populated only when Driver == DriverSQLite
	SQLiteLogsPath string // populated only when Driver == DriverSQLite
}

// ResolveDatabase parses the CLAWORC_DATABASE URL (or returns a SQLite
// fallback when the URL is empty) and returns the resolved driver,
// dialectors, and pool config.
//
// Empty url:
//
//	→ SQLite at <dataDir>/claworc.db and <dataDir>/llm-logs.db.
//
// "sqlite:///abs/path/main.db" or "sqlite://relative.db":
//
//	→ SQLite at the given path; the logs DB is the sibling file
//	  named "llm-logs.db" in the same directory.
//
// "postgres://user:pass@host:port/dbname?sslmode=...":
//
//	→ Postgres; main and logs share the connection.
//
// "mysql://user:pass@host:port/dbname" or "mariadb://...":
//
//	→ MySQL/MariaDB; main and logs share the connection.
//
// Pool tuning may be overridden via query params on the URL:
//
//	max_open_conns, max_idle_conns, conn_max_lifetime (Go duration).
//
// These params are stripped before the DSN is handed to the driver.
func ResolveDatabase(rawURL, dataDir string) (*ResolvedDB, error) {
	pool := DefaultPoolConfig()

	if rawURL == "" {
		mainPath := filepath.Join(dataDir, "claworc.db")
		logsPath := filepath.Join(dataDir, "llm-logs.db")
		return &ResolvedDB{
			Driver:         DriverSQLite,
			MainDialector:  sqlite.Open(mainPath),
			LogsDialector:  sqlite.Open(logsPath),
			ShareConn:      false,
			Pool:           pool,
			SQLiteMainPath: mainPath,
			SQLiteLogsPath: logsPath,
		}, nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse CLAWORC_DATABASE: %w", err)
	}

	// Pull pool tuning out of the query string so it doesn't leak into DSNs.
	q := u.Query()
	if v := q.Get("max_open_conns"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			pool.MaxOpenConns = n
		}
		q.Del("max_open_conns")
	}
	if v := q.Get("max_idle_conns"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			pool.MaxIdleConns = n
		}
		q.Del("max_idle_conns")
	}
	if v := q.Get("conn_max_lifetime"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			pool.ConnMaxLifetime = d
		}
		q.Del("conn_max_lifetime")
	}
	u.RawQuery = q.Encode()

	switch strings.ToLower(u.Scheme) {
	case "sqlite", "sqlite3", "file":
		path := sqliteURLPath(u)
		if path == "" {
			return nil, fmt.Errorf("sqlite URL missing path: %s", rawURL)
		}
		logsPath := filepath.Join(filepath.Dir(path), "llm-logs.db")
		return &ResolvedDB{
			Driver:         DriverSQLite,
			MainDialector:  sqlite.Open(path),
			LogsDialector:  sqlite.Open(logsPath),
			ShareConn:      false,
			Pool:           pool,
			SQLiteMainPath: path,
			SQLiteLogsPath: logsPath,
		}, nil

	case "postgres", "postgresql":
		// gorm/driver/postgres accepts the URL form directly.
		dsn := u.String()
		dial := postgres.Open(dsn)
		return &ResolvedDB{
			Driver:        DriverPostgres,
			MainDialector: dial,
			LogsDialector: dial,
			ShareConn:     true,
			Pool:          pool,
		}, nil

	case "mysql", "mariadb":
		dsn, err := mysqlURLToDSN(u)
		if err != nil {
			return nil, err
		}
		dial := mysql.Open(dsn)
		return &ResolvedDB{
			Driver:        DriverMySQL,
			MainDialector: dial,
			LogsDialector: dial,
			ShareConn:     true,
			Pool:          pool,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported database driver %q (expected sqlite, postgres, mysql)", u.Scheme)
	}
}

// sqliteURLPath extracts the filesystem path from a sqlite URL. Both
// "sqlite:///abs/path.db" and "sqlite://./relative.db" are accepted, plus the
// degenerate "sqlite:relative.db" form.
func sqliteURLPath(u *url.URL) string {
	if u.Path != "" {
		return u.Path
	}
	if u.Opaque != "" {
		return u.Opaque
	}
	return u.Host
}

// mysqlURLToDSN converts a mysql:// URL into the keyword DSN format accepted
// by go-sql-driver/mysql, which is what gorm/driver/mysql expects.
//
// Example:
//
//	mysql://user:pass@host:3306/dbname?tls=true
//	→ user:pass@tcp(host:3306)/dbname?tls=true&parseTime=true&loc=UTC
func mysqlURLToDSN(u *url.URL) (string, error) {
	if u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("mysql URL missing database name: %s", u.Redacted())
	}
	dbname := strings.TrimPrefix(u.Path, "/")

	var auth string
	if u.User != nil {
		auth = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			auth += ":" + pw
		}
		auth += "@"
	}

	host := u.Host
	if host == "" {
		host = "127.0.0.1:3306"
	} else if !strings.Contains(host, ":") {
		host += ":3306"
	}

	q := u.Query()
	// parseTime=true is required for time.Time columns; loc=UTC keeps
	// behavior consistent with the SQLite/Postgres paths.
	if q.Get("parseTime") == "" {
		q.Set("parseTime", "true")
	}
	if q.Get("loc") == "" {
		q.Set("loc", "UTC")
	}

	dsn := fmt.Sprintf("%stcp(%s)/%s", auth, host, dbname)
	if enc := q.Encode(); enc != "" {
		dsn += "?" + enc
	}
	return dsn, nil
}
