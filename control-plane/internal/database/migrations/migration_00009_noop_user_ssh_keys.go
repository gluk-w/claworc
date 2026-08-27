package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

// 00009_noop_user_ssh_keys: registry placeholder for the new UserSSHKey
// model (user_ssh_keys table) added for the inbound SSH gateway feature.
//
// Per docs/migrations.md, a brand new table is handled by AutoMigrateAll
// on boot and does not require a Goose migration. However, the CI
// "Migration Drift Check" guard in .github/workflows/control-plane.yml
// errors out whenever models/models.go changes without a new migration file,
// so we register a no-op here to satisfy that guard and keep the goose
// registry contiguous.
func init() {
	register(&goose.Migration{
		Version: 9,
		Source:  "00009_noop_user_ssh_keys.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
	})
}
