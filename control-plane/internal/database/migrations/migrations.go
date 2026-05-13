// Package migrations holds all versioned Go migrations for the control
// plane database, plus the SQL files embedded for historical reasons.
//
// Each migration_NNNNN_*.go file uses init() to append a *goose.Migration
// to the package-private registry. The database package reads the
// registry via All() and registers everything with goose's global
// registry on each RunMigrations call. The database package also passes
// the live *gorm.DB and dialect string in via Configure() — migrations
// pull the DB they need from the package globals at run time, which
// keeps this subpackage from having to import the database package and
// breaking the cycle.
package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/pressly/goose/v3"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// allMigrations is the per-process registry of Go migrations. Each
// migration_NNNNN_*.go file appends to it via register() inside init().
var allMigrations []*goose.Migration

func register(m *goose.Migration) {
	for _, existing := range allMigrations {
		if existing.Version == m.Version {
			panic(fmt.Sprintf("duplicate Go migration version %d (%s vs %s)",
				m.Version, existing.Source, m.Source))
		}
	}
	allMigrations = append(allMigrations, m)
}

// All returns a stable-ordered snapshot of every Go migration declared in
// this package. database.RunMigrations calls this and re-registers with
// goose on each invocation.
func All() []*goose.Migration {
	out := make([]*goose.Migration, len(allMigrations))
	copy(out, allMigrations)
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

// runtime carries the database handle and dialect string that
// migrations need at apply time. It's set by Configure() from
// database.RunMigrations before goose runs.
var runtime struct {
	db     *gorm.DB
	driver string // "sqlite" | "postgres" | "mysql"
}

// Configure wires the live database connection into this subpackage so
// the migration callbacks can access it. The database package calls this
// once per RunMigrations invocation before goose.UpContext.
func Configure(db *gorm.DB, driver string) {
	runtime.db = db
	runtime.driver = driver
}

// DB returns the GORM handle previously installed by Configure. Migration
// callbacks call this to drive AutoMigrate or perform data backfills.
func DB() *gorm.DB { return runtime.db }

// Driver returns the active driver name, used by WithMigrator to pick
// the right GORM dialector for the goose-managed transaction.
func Driver() string { return runtime.driver }

// WithMigrator opens a GORM session bound to the goose-managed *sql.Tx
// and invokes fn with both the GORM Migrator and the underlying *gorm.DB.
// The session participates in the same transaction as goose's bookkeeping
// — a failure rolls back both the schema change and the goose_db_version
// row.
//
// New Go migrations should prefer this helper over raw tx.ExecContext so
// that DDL is expressed via the portable GORM Migrator interface
// (AddColumn, DropColumn, CreateTable, CreateIndex, etc.) — see
// docs/migrations.md.
func WithMigrator(ctx context.Context, tx *sql.Tx, fn func(gorm.Migrator, *gorm.DB) error) error {
	var dialector gorm.Dialector
	switch runtime.driver {
	case "sqlite":
		dialector = sqlite.New(sqlite.Config{Conn: tx})
	case "postgres":
		dialector = postgres.New(postgres.Config{Conn: tx})
	case "mysql":
		dialector = mysql.New(mysql.Config{Conn: tx})
	default:
		return fmt.Errorf("WithMigrator: unsupported driver %q (did database.RunMigrations call Configure?)", runtime.driver)
	}
	gdb, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return fmt.Errorf("open gorm over tx: %w", err)
	}
	return fn(gdb.WithContext(ctx).Migrator(), gdb.WithContext(ctx))
}
