package main

import (
	"log"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// reconcileStuckTasks runs once at startup. It finds DB rows that were left
// in a transient state by an earlier process that crashed/restarted before
// its goroutine could finish, and marks them terminal so the UI is not
// stuck (see docs/task-manager.md and issue #89).
//
// In-memory tasks from the previous process are gone — these stale rows are
// the only evidence that work was in flight. We mark them as failed/error
// with a clear message so the user can retry.
func reconcileStuckTasks() {
	if database.DB == nil {
		return
	}

	const reason = "process restarted before task completed"

	transientInstanceStates := []string{"creating", "restarting", "stopping", "deleting"}
	res := database.DB.Model(&database.Instance{}).
		Where("status IN ?", transientInstanceStates).
		Update("status", "error")
	if res.Error != nil {
		log.Printf("reconcileStuckTasks: instance sweep failed: %v", res.Error)
	} else if res.RowsAffected > 0 {
		log.Printf("reconcileStuckTasks: marked %d instance(s) as error (%s)", res.RowsAffected, reason)
	}

	res = database.DB.Model(&database.Backup{}).
		Where("status = ?", "running").
		Updates(map[string]interface{}{
			"status":        "failed",
			"error_message": reason,
		})
	if res.Error != nil {
		log.Printf("reconcileStuckTasks: backup sweep failed: %v", res.Error)
	} else if res.RowsAffected > 0 {
		log.Printf("reconcileStuckTasks: marked %d backup(s) as failed (%s)", res.RowsAffected, reason)
	}
}
