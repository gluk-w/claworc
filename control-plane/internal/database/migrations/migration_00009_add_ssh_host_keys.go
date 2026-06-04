package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

// 00009_add_ssh_host_keys: registry placeholder for the ssh_host_keys table
// added to persist SSH host keys across platform restarts (TOFU persistence).
//
// The table is created by AutoMigrateAll on boot — no DDL needed here. This
// file satisfies the CI "Migration Drift Check" guard that requires a new
// migration whenever models/models.go changes.
func init() {
	register(&goose.Migration{
		Version: 9,
		Source:  "00009_add_ssh_host_keys.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
	})
}
