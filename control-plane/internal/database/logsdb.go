package database

import (
	"fmt"
	"os"

	"gorm.io/gorm"
)

// LogsDB holds LLM request logs. For non-SQLite drivers this points to the
// same *gorm.DB as DB so all tables live in a single database; SQLite keeps
// a dedicated file for backwards compatibility.
var LogsDB *gorm.DB

func InitLogsDB(dataPath string) error {
	if resolved == nil {
		return fmt.Errorf("InitLogsDB called before Init")
	}

	if resolved.ShareConn {
		// Postgres / MySQL: re-use the main connection. AutoMigrate the logs
		// model onto the same DB so llm_request_logs lives alongside everything
		// else.
		LogsDB = DB
		if err := LogsDB.AutoMigrate(&LLMRequestLog{}); err != nil {
			return fmt.Errorf("auto-migrate logs DB: %w", err)
		}
		return nil
	}

	// SQLite: open the dedicated logs file.
	if dataPath != "" {
		if err := os.MkdirAll(dataPath, 0755); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}
	}

	var err error
	LogsDB, err = openDialector(resolved.LogsDialector, resolved.Driver, resolved.Pool)
	if err != nil {
		return fmt.Errorf("open logs database: %w", err)
	}

	if err := LogsDB.AutoMigrate(&LLMRequestLog{}); err != nil {
		return fmt.Errorf("auto-migrate logs DB: %w", err)
	}

	return nil
}
