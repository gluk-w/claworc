package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00010_add_cf_ai_gateway_token: add the CfAIGatewayToken column to llm_providers.
//
// CfAIGatewayToken holds the Fernet-encrypted Cloudflare AI Gateway
// authentication token, forwarded as `cf-aig-authorization` for Authenticated
// gateways (api_type cloudflare-ai-gateway; see docs/virtual-keys.md). It is an
// additive, defaulted column.
//
// Needed on upgrade from earlier deployments where the v1 baseline was stamped
// against a model set that didn't declare CfAIGatewayToken — goose skips the
// now-updated v1 body, so the column must be added by a delta. Fresh installs
// land via v1's AutoMigrate against the current model set, which already
// creates the column; the HasColumn guard below turns this migration into a
// no-op in that case.
func init() {
	register(&goose.Migration{
		Version: 10,
		Source:  "00010_add_cf_ai_gateway_token.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if !m.HasColumn(&models.LLMProvider{}, "CfAIGatewayToken") {
					if err := m.AddColumn(&models.LLMProvider{}, "CfAIGatewayToken"); err != nil {
						return err
					}
				}
				return nil
			})
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if m.HasColumn(&models.LLMProvider{}, "CfAIGatewayToken") {
					if err := m.DropColumn(&models.LLMProvider{}, "CfAIGatewayToken"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	})
}
