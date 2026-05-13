package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

// 00002_noop: registry placeholder.
//
// The original 00002_placeholder.sql was a `SELECT 1;` no-op that left a
// stamped row in `goose_db_version` on every existing install. When the
// migration set was rewritten in Go, the SQL file was removed but the
// stamped row remains on upgraded databases. Re-introducing v2 as a
// registered no-op keeps goose's registry contiguous so the orphaned row
// matches a known migration instead of generating "missing migration"
// warnings on every startup.
func init() {
	register(&goose.Migration{
		Version: 2,
		Source:  "00002_noop.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return nil
		},
	})
}
