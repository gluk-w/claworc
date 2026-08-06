package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

// 00013_noop_instance_slack_config: registry placeholder for the
// Instance.SlackConfig column added for the per-agent Slack connection
// feature (see docs/slack-connections.md).
//
// Per docs/migrations.md, additive column changes are handled by
// AutoMigrateAll on boot and do not require a Goose migration. However,
// the CI "Migration Drift Check" guard in .github/workflows/control-plane.yml
// errors out whenever models/models.go changes without a new migration file,
// so we register a no-op here to satisfy that guard and keep the goose
// registry contiguous.
func init() {
	register(&goose.Migration{
		Version: 13,
		Source:  "00013_noop_instance_slack_config.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
	})
}
