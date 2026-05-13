# Database migrations

The control plane manages its schema with two layers that run at startup, in this order:

1. **`AutoMigrateAll`** — GORM's `AutoMigrate` against every model in `control-plane/internal/database/models/models.go`. Runs on **every boot**, both fresh installs and upgrades. Adds new tables, new columns, and new indexes for free.
2. **Goose migrations** — versioned Go files under `control-plane/internal/database/migrations/`. Embedded in the binary. Apply only the *non-additive* changes AutoMigrate can't handle, plus data backfills/seeds.

Both are invoked from `database.RunMigrations` (see `control-plane/internal/database/migrations.go`); there is no separate "migrate" step in production.

## TL;DR: do I need a migration?

| Change in `models/models.go`                  | Needs a migration?                              |
|-----------------------------------------------|-------------------------------------------------|
| Add a new table (new struct in `models.go`)   | **No** — AutoMigrate creates it on next boot.   |
| Add a new column                              | **No** — AutoMigrate adds it on next boot.      |
| Add a new index (via `gorm:"index"` tag)      | **No** — AutoMigrate creates it on next boot.   |
| Change a column's type                        | **Yes** — AutoMigrate won't ALTER COLUMN.       |
| Drop a column                                 | **Yes** — AutoMigrate is additive-only.         |
| Rename a column or table                      | **Yes** — AutoMigrate sees rename as add+drop.  |
| Populate / backfill row data                  | **Yes** — schema alone can't seed values.       |
| Tighten a NOT NULL or unique constraint       | **Yes** — usually needs a backfill first.       |

If your change is in the "No" rows, **just edit `models.go` and ship it**. Don't run `make migration`. The next boot of every install will pick the schema change up automatically.

If your change is in the "Yes" rows, run `make migration` and follow the prompts from the `migration-author` Claude Code subagent.

## What runs on boot

`database.RunMigrations` does, in order:

1. `migrations.Configure(DB, driver)` — hands the live `*gorm.DB` to the migrations subpackage so callbacks can use `DB()` and `WithMigrator`.
2. `migrations.AutoMigrateAll(DB)` — runs `gdb.AutoMigrate(&models.Instance{}, &models.Setting{}, ...)` over the full model list. Idempotent. Adds anything missing.
3. `goose.UpContext` — applies any registered Go migrations whose version isn't yet stamped in `goose_db_version`. The `WithAllowMissing()` option is set, so migrations are tolerated when an older version is unapplied but a newer one is applied (handles dev DBs that ran intermediate branch states).

The v1 baseline (`migration_00001_baseline.go`) is intentionally a no-op now; it exists so existing installs don't see a missing-migration warning for the row they already stamped.

## Conventions

### File layout

```
control-plane/internal/database/
  database.go               # connection, Init, helpers
  migrations.go             # RunMigrations: AutoMigrateAll + goose
  models.go                 # type aliases (Instance = models.Instance, ...)
  models/
    models.go               # canonical GORM struct definitions — source of truth
  migrations/
    migrations.go           # registry, Configure(), WithMigrator helper
    migration_00001_baseline.go     # no-op marker (kept for goose continuity)
    migration_00002_noop.go         # no-op marker (replaces the original SQL placeholder)
    migration_00003_create_teams.go # historical: pre-policy, now redundant w/ AutoMigrate
    migration_00004_seed_teams.go   # data backfill (canonical use-case for a migration)
    migration_00005_add_team_ids.go # historical: pre-policy add-column, kept for upgrade safety
    migration_NNNNN_<name>.go       # ← new non-additive / data migrations land here
```

Each new migration file:

- Uses `package migrations`.
- Imports `github.com/gluk-w/claworc/control-plane/internal/database/models` for model types.
- Contains exactly one `init()` that calls `register(&goose.Migration{...})`.
- Names itself `migration_NNNNN_<snake_case>.go` where `NNNNN` is the zero-padded next-highest version.

The `Source` field on the `goose.Migration` must match the file basename — goose uses it for logs and the `goose_db_version` table.

### Anatomy of a migration (data backfill — the canonical case)

```go
package migrations

import (
    "context"
    "database/sql"
    "fmt"

    "github.com/pressly/goose/v3"
    "gorm.io/gorm"

    "github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00007_backfill_instance_foo: populate Instance.Foo for rows where it
// is still empty, using LegacyFoo as the source. AutoMigrate has already
// created the Foo column by the time this runs.
func init() {
    register(&goose.Migration{
        Version: 7,
        Source:  "00007_backfill_instance_foo.go",
        UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
            return WithMigrator(ctx, tx, func(m gorm.Migrator, gdb *gorm.DB) error {
                return gdb.Model(&models.Instance{}).
                    Where("foo IS NULL OR foo = ''").
                    Update("foo", gorm.Expr("legacy_foo")).Error
            })
        },
        DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
            return fmt.Errorf("not reversible: data backfill")
        },
    })
}
```

### Anatomy of a migration (column type change — the second canonical case)

```go
// 00008_change_user_points_to_bigint: User.Points outgrew int32. AutoMigrate
// won't ALTER COLUMN, so this migration explicitly recreates the column with
// the wider type and copies values over.
func init() {
    register(&goose.Migration{
        Version: 8,
        Source:  "00008_change_user_points_to_bigint.go",
        UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
            // ... raw SQL via tx.ExecContext, dialect-aware ...
        },
        DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
            return fmt.Errorf("not reversible: narrowing type would truncate")
        },
    })
}
```

`WithMigrator` opens a GORM session over the goose-managed `*sql.Tx`, so DDL and goose's bookkeeping commit or roll back together. `register` adds the migration to the subpackage registry; `RunMigrations` then re-registers everything with goose's global list before running goose Up.

### GORM Migrator cheat-sheet

Authoritative reference: <https://gorm.io/docs/migration.html#Migrator-Interface>

Most-used operations when you DO need a migration:

| Operation               | Call                                                       |
|-------------------------|------------------------------------------------------------|
| Drop a column           | `m.DropColumn(&models.Model{}, "FieldName")`               |
| Rename a column         | `m.RenameColumn(&models.Model{}, "OldName", "NewName")`    |
| Check column existence  | `m.HasColumn(&models.Model{}, "FieldName")`                |
| Drop a table            | `m.DropTable(&models.Model{})`                             |
| Rename a table          | `m.RenameTable(&Old{}, &New{})`                            |
| Check table existence   | `m.HasTable(&models.Model{})`                              |
| Drop an index           | `m.DropIndex(&models.Model{}, "FieldName")`                |
| Check index existence   | `m.HasIndex(&models.Model{}, "FieldName")`                 |

`AddColumn`, `CreateTable`, and `CreateIndex` exist but you almost never need them — AutoMigrate already runs them on every boot. Reach for them only if you must combine an additive change with a backfill in the same transaction.

**Field arguments use the Go field name** (e.g. `"TeamIDs"`), not the column name (`"team_ids"`). The Migrator resolves field → column through GORM's NamingStrategy — the same path AutoMigrate uses.

### Idempotence

Data backfills should be safely re-runnable. Use `WHERE col IS NULL`, `WHERE NOT EXISTS`, `INSERT ... ON CONFLICT DO NOTHING` (Postgres/SQLite), or count-then-create patterns. `migration_00004_seed_teams.go` is the canonical example.

For migrations that do call Migrator DDL (drops/renames), guard with `HasColumn` / `HasTable` / `HasIndex` so a re-run is safe — operators sometimes manually replay migrations, and `WithAllowMissing` plus dev DB resets can cause the same migration to run on a state that already matches.

### Down migrations

Implement `DownFnContext` when feasible. Return `fmt.Errorf("not reversible")` for destructive or lossy reversals. The control plane has no production "migrate down" flow; downs are insurance for operators recovering manually.

### The raw-SQL exception

For column type changes, complex constraint juggling, or anything else the Migrator can't express, use `tx.ExecContext(ctx, "...")` directly. Add a comment explaining why the Migrator doesn't suffice and, where the DDL differs by driver, branch on `Driver()` (sqlite/postgres/mysql).

## Tooling

### `make migration`

Invokes the `migration-author` subagent (defined at `.claude/agents/migration-author.md`). You must tell the agent what the migration is for — it does **not** synthesize migrations from a model diff under the new policy. Example prompts the agent accepts:

- "backfill `Instance.foo` from `Instance.legacy_foo` for rows where foo is empty"
- "change `User.Points` from int to int64"
- "drop `Instance.DeprecatedThing`"
- "rename `team_members.role` to `team_members.member_role`"

If you ran `make migration` because you added a new column or table to `models.go`, the agent will tell you AutoMigrate already handles it and exit without writing a file.

### `make migration-check`

Runs `go run ./cmd/migrationcheck`. Applies AutoMigrate + every registered migration to a fresh in-memory SQLite database and asserts every model has its table + columns. Useful as a smoke check that AutoMigrate + your migration set still cover the model graph.

Use `-dump` to print the resulting schema as JSON.

## Rules

- **Never edit a migration file after it has shipped.** Once a version has been applied in any environment, its content is part of history. Add a new versioned file instead.
- **New migrations must be Go.** Do not add SQL files to `internal/database/migrations/`.
- **Don't write migrations for additive changes.** AutoMigrate handles them. Writing a migration for a new column is duplicate work and accretes noise in `goose_db_version`.
- **Down migrations are best-effort, not load-bearing.** Implement when easy; `return fmt.Errorf("not reversible")` is fine for destructive ones.
