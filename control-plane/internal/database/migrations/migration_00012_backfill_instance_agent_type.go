package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00012_backfill_instance_agent_type: populates Instance.AgentType for rows
// that pre-date the column. AutoMigrateAll creates the column on every boot;
// this migration walks rows where agent_type IS NULL OR ” and stamps the
// only agent type that existed before the universal agent shim: "openclaw".
// Idempotent: re-runs match zero rows.
func init() {
	register(&goose.Migration{
		Version: 12,
		Source:  "00012_backfill_instance_agent_type.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, gdb *gorm.DB) error {
				return gdb.Model(&models.Instance{}).
					Where("agent_type IS NULL OR agent_type = ''").
					Update("agent_type", models.AgentTypeOpenClaw).Error
			})
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return fmt.Errorf("backfill_instance_agent_type migration is not reversible")
		},
	})
}
