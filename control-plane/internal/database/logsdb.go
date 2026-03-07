package database

import (
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var LogsDB *gorm.DB

func InitLogsDB(dataPath string) error {
	if dataPath != "" {
		if err := os.MkdirAll(dataPath, 0755); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}
	}
	dbPath := filepath.Join(dataPath, "llm-logs.db")

	var err error
	LogsDB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("open logs database: %w", err)
	}

	sqlDB, err := LogsDB.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("set busy timeout: %w", err)
	}

	if err := LogsDB.AutoMigrate(&LLMRequestLog{}); err != nil {
		return fmt.Errorf("auto-migrate logs DB: %w", err)
	}

	return nil
}
