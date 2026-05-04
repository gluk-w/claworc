# Databases

## Overview

Claworc's control plane stores application state in a relational database. The
default backend is SQLite (file-backed, zero-config), and PostgreSQL and
MariaDB/MySQL are also supported for production deployments that prefer an
external managed database.

Configuration is driven by a single environment variable, **`CLAWORC_DATABASE`**,
whose value is a URL describing the driver, credentials, host, and database
name. When unset, the control plane falls back to SQLite under
`CLAWORC_DATA_PATH`.

## URL format

```
sqlite:///absolute/path/to/main.db
postgres://user:pass@host:5432/dbname?sslmode=require
mysql://user:pass@host:3306/dbname
mariadb://user:pass@host:3306/dbname     # alias for mysql://
```

Examples:

| Goal | URL |
|------|-----|
| Default (no env var set) | *(unset)* — SQLite under `CLAWORC_DATA_PATH` |
| Explicit SQLite path | `sqlite:///var/lib/claworc/main.db` |
| Local Postgres | `postgres://claworc:secret@localhost:5432/claworc` |
| Managed Postgres with TLS | `postgres://claworc:secret@db.example.com:5432/claworc?sslmode=require` |
| MariaDB | `mariadb://claworc:secret@db.example.com:3306/claworc` |

Unsupported schemes (anything other than `sqlite`, `postgres`/`postgresql`,
`mysql`, `mariadb`) cause the control plane to fail fast at startup with a
clear error.

### Pool tuning

Server-class drivers (Postgres / MySQL) accept three optional query-string
parameters that override the pool defaults. They are stripped from the DSN
before being handed to the driver, so they are safe to mix with normal driver
options:

| Param | Default | Notes |
|-------|---------|-------|
| `max_open_conns` | `20` | Hard cap on simultaneously open connections. |
| `max_idle_conns` | `5` | Idle connections kept warm in the pool. |
| `conn_max_lifetime` | `1h` | Go duration; rotates connections to play nicely with PgBouncer / proxysql. |

Example: `postgres://u:p@h/d?sslmode=require&max_open_conns=50&conn_max_lifetime=15m`.

SQLite ignores these (the file-backed connection is effectively single-pool)
and instead pins `PRAGMA busy_timeout=5000` to ride out write contention.

## Topology — single DB or two

The control plane logically uses two databases:

1. **Main** — instances, users, settings, kanban, backups, browser sessions, …
2. **LLM logs** — high-volume per-request usage / cost / latency rows.

| Driver | Layout |
|--------|--------|
| SQLite | Two files: `<DATA_PATH>/claworc.db` and `<DATA_PATH>/llm-logs.db`. Keeps backwards compatibility with all pre-existing installs. |
| Postgres / MySQL / MariaDB | Single database; the `llm_request_logs` table sits alongside everything else. The control plane opens one connection and reuses it for both code paths. |

This means you only need to provision one database when running on Postgres or
MariaDB. If you want to retire an existing per-database split, simply restore
both SQLite files into the same target database and point `CLAWORC_DATABASE`
at it.

## Schema migrations

Schema management uses [goose](https://github.com/pressly/goose) running
against the active driver:

- A baseline migration (`00001_baseline.go`) is registered programmatically
  and delegates to GORM AutoMigrate, which materializes all 17 application
  tables across SQLite, Postgres, and MySQL alike. Idempotent on existing
  installs that already have the schema.
- Future schema deltas land as numbered SQL files under
  `control-plane/internal/database/migrations/`. The files are embedded into
  the binary (`//go:embed`), so the control plane never reaches out to the
  filesystem at runtime.
- On every startup, `Init()` calls `RunMigrations(ctx)` after opening the
  connection. Any pending migration is applied; the goose bookkeeping table
  (`goose_db_version`) tracks what has been applied.

For driver-specific SQL, prefer additive changes that work everywhere
(`CREATE TABLE IF NOT EXISTS`, generic column types) and split into per-driver
files only when truly necessary. The codebase is committed to staying
dialect-portable for application code; new raw SQL should pass review against
all three engines.

## Operational notes

### Backups

- **SQLite** — back up the `<DATA_PATH>` directory; both `claworc.db` and
  `llm-logs.db` live there. Online copies should use `sqlite3 .backup` or
  filesystem snapshots.
- **Postgres** — use `pg_dump`/`pg_restore` against the configured database.
  No special tables are external; the goose `goose_db_version` table is
  included automatically.
- **MariaDB** — `mysqldump --single-transaction --routines` against the
  configured database.

The `CLAWORC_BACKUPS_PATH` directory is for **instance** backups (per-OpenClaw
home/homebrew/data archives), not control-plane database backups. Those two
concerns are independent — back up the control-plane DB through your normal
DBA tooling.

### Migrating between drivers

There is no built-in cross-driver dump/restore. If you outgrow SQLite:

1. Stop the control plane.
2. Use a generic dump tool (e.g. `sqlite3 .dump`, hand-edit, then `psql -f`)
   to export from SQLite and import into Postgres/MySQL. Schema differences
   are minor — GORM's AutoMigrate writes the same logical types everywhere.
3. Set `CLAWORC_DATABASE` and restart.

For one-time loads, the simplest approach is to restart the control plane
pointed at the new database (so AutoMigrate creates an empty schema), then
copy data table-by-table with the dump/restore tooling of your choice.

### TLS

- **Postgres** — append `?sslmode=require` (or `verify-full` with a CA bundle
  configured via standard libpq env vars).
- **MySQL/MariaDB** — append `?tls=true` (or `tls=preferred`,
  `tls=skip-verify`); see the
  [go-sql-driver TLS docs](https://github.com/go-sql-driver/mysql#tls).

### Connection limits and pgbouncer

The default pool cap is 20 open connections; increase via
`max_open_conns` if you scale the control plane horizontally (rare today; the
control plane runs as a single replica). When running behind pgbouncer in
**transaction** mode, set `conn_max_lifetime` shorter than the pgbouncer
`server_idle_timeout` to avoid stale-connection errors.

## Helm

The Helm chart exposes the URL via a `database` block in `values.yaml`:

```yaml
database:
  url: ""               # e.g. postgres://user:pass@host:5432/claworc
  existingSecret: ""    # Secret name with a "url" key; takes precedence over .url
```

When neither field is set, the chart preserves the default behavior (SQLite on
the data PVC). The data PVC is **always** mounted because `/app/data` also
holds SSH keys, backup staging, and other on-disk artifacts that are unrelated
to the relational database.

For production deployments using an external database, prefer
`existingSecret` so credentials never appear in the values file:

```bash
kubectl create secret generic claworc-db \
  --from-literal=url='postgres://claworc:secret@db.example.com:5432/claworc?sslmode=require'

helm upgrade claworc helm/ --set database.existingSecret=claworc-db
```

## Source map

| File | Responsibility |
|------|----------------|
| `control-plane/internal/config/config.go` | `Database` field bound to `CLAWORC_DATABASE` env var. |
| `control-plane/internal/database/url.go` | URL parser: drivers, pool tuning, dialector construction. |
| `control-plane/internal/database/database.go` | `Init()`, connection lifecycle, default seeding. |
| `control-plane/internal/database/logsdb.go` | LLM logs DB lifecycle; reuses the main connection on Postgres/MySQL. |
| `control-plane/internal/database/migrations.go` | Goose driver, baseline registration, embedded SQL loader. |
| `control-plane/internal/database/migrations/` | SQL delta migrations. |
| `helm/values.yaml`, `helm/templates/deployment.yaml` | `database.url` / `existingSecret` → `CLAWORC_DATABASE`. |
