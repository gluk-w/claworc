package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

// 00015_noop_browser_passkeys: registry placeholder for the new
// browser_passkeys table and the instances.virtual_authenticator column
// (virtual WebAuthn authenticator, see docs/virtual-authenticator.md).
//
// Per docs/migrations.md, new tables and additive columns are handled by
// AutoMigrateAll on boot and do not require a Goose migration. The CI
// "Migration Drift Check" guard errors whenever models/models.go changes
// without a new migration file, so we register a no-op here to satisfy it
// and keep the goose registry contiguous.
func init() {
	register(&goose.Migration{
		Version: 15,
		Source:  "00015_noop_browser_passkeys.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
	})
}
