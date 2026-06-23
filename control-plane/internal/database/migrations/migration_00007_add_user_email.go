package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00007_add_user_email: add the Email column to users.
//
// Email is used to match Cloudflare Access (Zero Trust) identities to a user
// (see docs/auth.md). It is an additive, nullable/defaulted column with a plain
// (non-unique) index — uniqueness among populated values is enforced in the
// application layer, since existing users have an empty email that a SQL UNIQUE
// constraint would treat as colliding.
//
// Needed on upgrade from earlier deployments where the v1 baseline was stamped
// against a model set that didn't declare Email — goose skips the now-updated v1
// body, so the column must be added by a delta. Fresh installs land via v1's
// AutoMigrate against the current model set, which already creates the column;
// the HasColumn guard below turns this migration into a no-op in that case.
func init() {
	register(&goose.Migration{
		Version: 7,
		Source:  "00007_add_user_email.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if !m.HasColumn(&models.User{}, "Email") {
					if err := m.AddColumn(&models.User{}, "Email"); err != nil {
						return err
					}
				}
				if !m.HasIndex(&models.User{}, "Email") {
					if err := m.CreateIndex(&models.User{}, "Email"); err != nil {
						return err
					}
				}
				return nil
			})
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(m gorm.Migrator, _ *gorm.DB) error {
				if m.HasColumn(&models.User{}, "Email") {
					if err := m.DropColumn(&models.User{}, "Email"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	})
}
