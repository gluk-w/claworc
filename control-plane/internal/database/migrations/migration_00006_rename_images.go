package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

// 00006_rename_images: rewrites stored Docker image references from the
// retired `glukw/claworc-agent` and `glukw/claworc-browser-<variant>`
// namespaces to the new `claworc/openclaw` and `claworc/<variant>-browser`
// org-scoped names.
//
// Touched columns:
//   - instances.container_image
//   - instances.browser_image
//   - settings.value where key in ('default_agent_image', 'default_browser_image')
//
// Idempotent: re-running on already-migrated rows is a no-op because the
// old prefix is gone after the first pass and the LIKE filters would not
// match a second time. Safe to leave applied even if some rows were
// already on the new names.
func init() {
	register(&goose.Migration{
		Version: 6,
		Source:  "00006_rename_images.go",
		UpFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return WithMigrator(ctx, tx, func(_ gorm.Migrator, gdb *gorm.DB) error {
				return renameImages(gdb)
			})
		},
		DownFnContext: func(ctx context.Context, tx *sql.Tx) error {
			return fmt.Errorf("rename_images migration is not reversible")
		},
	})
}

// renameImages performs the in-place REPLACE rewrites. Exposed for tests.
//
// Statements run via the GORM model API so column quoting is dialect-aware
// (the settings table has a `key` column, which is reserved in MySQL and
// Postgres). REPLACE() is supported by SQLite, MySQL, and Postgres.
func renameImages(gdb *gorm.DB) error {
	if err := gdb.Model(&models.Instance{}).
		Where("container_image LIKE ?", "%glukw/claworc-agent%").
		Update("container_image", gorm.Expr("REPLACE(container_image, ?, ?)", "glukw/claworc-agent", "claworc/openclaw")).Error; err != nil {
		return fmt.Errorf("rename instances.container_image: %w", err)
	}
	for _, variant := range []string{"chromium", "chrome", "brave", "base"} {
		old := "glukw/claworc-browser-" + variant
		new_ := "claworc/" + variant + "-browser"
		if err := gdb.Model(&models.Instance{}).
			Where("browser_image LIKE ?", "%"+old+"%").
			Update("browser_image", gorm.Expr("REPLACE(browser_image, ?, ?)", old, new_)).Error; err != nil {
			return fmt.Errorf("rename instances.browser_image (%s): %w", variant, err)
		}
		if err := gdb.Model(&models.Setting{}).
			Where("key = ? AND value LIKE ?", "default_browser_image", "%"+old+"%").
			Update("value", gorm.Expr("REPLACE(value, ?, ?)", old, new_)).Error; err != nil {
			return fmt.Errorf("rename settings.default_browser_image (%s): %w", variant, err)
		}
	}
	if err := gdb.Model(&models.Setting{}).
		Where("key = ? AND value LIKE ?", "default_agent_image", "%glukw/claworc-agent%").
		Update("value", gorm.Expr("REPLACE(value, ?, ?)", "glukw/claworc-agent", "claworc/openclaw")).Error; err != nil {
		return fmt.Errorf("rename settings.default_agent_image: %w", err)
	}
	return nil
}
