package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

// 00014_noop_instance_discord_config: registry placeholder for the
// Instance.DiscordConfig column added for the per-agent Discord connection
// feature (see docs/discord-connections.md).
//
// Per docs/migrations.md, additive column changes are handled by
// AutoMigrateAll on boot and do not require a Goose migration. However,
// the CI "Migration Drift Check" guard in .github/workflows/control-plane.yml
// errors out whenever models/models.go changes without a new migration file,
// so we register a no-op here to satisfy that guard and keep the goose
// registry contiguous.
func init() {
	register(&goose.Migration{
		Version: 14,
		Source:  "00014_noop_instance_discord_config.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
	})
}
