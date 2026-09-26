package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
)

// 00012_rename_llm_proxy_keys: renames the legacy `llm_gateway_keys` table
// (and its `gateway_key` column) to `llm_proxy_keys` / `virtual_key`, part of
// the "LLM Gateway" → "Internal Proxy" terminology rename.
//
// Ordering note: AutoMigrateAll runs on every boot *before* goose migrations,
// so by the time this runs the new `llm_proxy_keys` table already exists (with
// the correct, new-named indexes) — empty on an upgrade, freshly created on a
// new install. We therefore copy rows from the old table into the AutoMigrated
// new table and drop the old one, rather than RENAME (which would collide with
// the table AutoMigrate just created and leave stale index names behind).
//
// Idempotent: a no-op when the old table is absent (fresh installs, or a second
// apply). The id column is intentionally not copied so auto-increment sequences
// stay consistent across drivers; LLMProxyKey.ID is not referenced by any FK.
func init() {
	register(&goose.Migration{
		Version: 12,
		Source:  "00012_rename_llm_proxy_keys.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, gdb *gorm.DB) error {
				return renameLLMProxyKeysUp(m, gdb)
			})
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, gdb *gorm.DB) error {
				return renameLLMProxyKeysDown(m, gdb)
			})
		},
	})
}

const (
	llmGatewayKeysTable = "llm_gateway_keys"
	llmProxyKeysTable   = "llm_proxy_keys"
)

// legacyLLMGatewayKey reproduces the pre-rename schema so the Down migration
// can recreate the old table portably via AutoMigrate.
type legacyLLMGatewayKey struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	InstanceID uint   `gorm:"not null;uniqueIndex:idx_lgk_inst_prov"`
	ProviderID uint   `gorm:"not null;uniqueIndex:idx_lgk_inst_prov"`
	GatewayKey string `gorm:"not null;uniqueIndex"`
}

func (legacyLLMGatewayKey) TableName() string { return llmGatewayKeysTable }

func renameLLMProxyKeysUp(m gorm.Migrator, gdb *gorm.DB) error {
	if !m.HasTable(llmGatewayKeysTable) {
		// Fresh install (or already migrated): AutoMigrateAll created
		// llm_proxy_keys and there is nothing to carry over.
		return nil
	}
	// AutoMigrateAll already created llm_proxy_keys with the new indexes; copy
	// the legacy rows across, mapping gateway_key → virtual_key.
	if err := gdb.Exec(fmt.Sprintf(
		`INSERT INTO %s (instance_id, provider_id, virtual_key)
		 SELECT instance_id, provider_id, gateway_key FROM %s`,
		llmProxyKeysTable, llmGatewayKeysTable)).Error; err != nil {
		return fmt.Errorf("copy rows %s → %s: %w", llmGatewayKeysTable, llmProxyKeysTable, err)
	}
	if err := m.DropTable(llmGatewayKeysTable); err != nil {
		return fmt.Errorf("drop %s: %w", llmGatewayKeysTable, err)
	}
	return nil
}

func renameLLMProxyKeysDown(m gorm.Migrator, gdb *gorm.DB) error {
	if !m.HasTable(llmProxyKeysTable) {
		return nil
	}
	// Recreate the legacy table (portable across drivers via AutoMigrate).
	if err := m.AutoMigrate(&legacyLLMGatewayKey{}); err != nil {
		return fmt.Errorf("recreate %s: %w", llmGatewayKeysTable, err)
	}
	if err := gdb.Exec(fmt.Sprintf(
		`INSERT INTO %s (instance_id, provider_id, gateway_key)
		 SELECT instance_id, provider_id, virtual_key FROM %s`,
		llmGatewayKeysTable, llmProxyKeysTable)).Error; err != nil {
		return fmt.Errorf("copy rows %s → %s: %w", llmProxyKeysTable, llmGatewayKeysTable, err)
	}
	if err := m.DropTable(llmProxyKeysTable); err != nil {
		return fmt.Errorf("drop %s: %w", llmProxyKeysTable, err)
	}
	return nil
}
