package database

import (
	"time"

	"gorm.io/gorm/clause"
)

// upsertSetting is the driver-portable INSERT-or-UPDATE for the settings
// table. GORM translates clause.OnConflict to:
//   - SQLite/Postgres → ON CONFLICT(key) DO UPDATE SET value=..., updated_at=...
//   - MySQL/MariaDB   → ON DUPLICATE KEY UPDATE value=VALUES(value), ...
func upsertSetting(key, value string, updatedAt time.Time) error {
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&Setting{Key: key, Value: value, UpdatedAt: updatedAt}).Error
}
