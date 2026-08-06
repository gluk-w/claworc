package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

// 00011_noop_sa_ports: registry placeholder for the new
// service_account_annotations and ports columns added to the Instance model
// (per-instance ServiceAccount + additional exposed ports for non-agent
// workloads).
//
// Per docs/migrations.md, additive columns are handled by AutoMigrateAll on
// boot and do not require a Goose migration. However, the CI "Migration
// Drift Check" guard in .github/workflows/control-plane.yml errors out
// whenever models/models.go changes without a new migration file, so we
// register a no-op here to satisfy that guard and keep the goose registry
// contiguous.
func init() {
	register(&goose.Migration{
		Version: 11,
		Source:  "00011_noop_sa_ports.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
	})
}
