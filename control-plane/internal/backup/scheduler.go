package backup

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/robfig/cron/v3"
)

// ComputeNextRun parses a cron expression and returns the next run time after now.
func ComputeNextRun(cronExpr string) (time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(time.Now().UTC()), nil
}

// StartScheduleExecutor starts a background goroutine that checks backup schedules
// every minute and triggers backups for due schedules.
func StartScheduleExecutor(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go runScheduleExecutor(ctx)
	return cancel
}

func runScheduleExecutor(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			executeDueSchedules(ctx)
		}
	}
}

func executeDueSchedules(ctx context.Context) {
	schedules, err := database.ListDueSchedules()
	if err != nil {
		log.Printf("backup scheduler: failed to list schedules: %v", err)
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		return
	}

	for _, s := range schedules {
		executeSchedule(ctx, orch, s)
	}
}

func executeSchedule(ctx context.Context, orch orchestrator.ContainerOrchestrator, s database.BackupSchedule) {
	instanceIDs, err := resolveScheduleInstances(s.InstanceIDs)
	if err != nil {
		log.Printf("backup scheduler: schedule %d: failed to resolve instances: %v", s.ID, err)
		return
	}

	var paths []string
	if s.Paths != "" {
		json.Unmarshal([]byte(s.Paths), &paths)
	}

	for _, instID := range instanceIDs {
		var inst database.Instance
		if err := database.DB.First(&inst, instID).Error; err != nil {
			log.Printf("backup scheduler: schedule %d: instance %d not found: %v", s.ID, instID, err)
			continue
		}
		if _, err := CreateFullBackup(ctx, orch, inst.Name, inst.ID, "scheduled", paths); err != nil {
			log.Printf("backup scheduler: schedule %d: backup for instance %s failed: %v", s.ID, inst.Name, err)
		}
	}

	// Update last run and compute next run
	now := time.Now().UTC()
	updates := map[string]interface{}{
		"last_run_at": &now,
	}
	if next, err := ComputeNextRun(s.CronExpression); err == nil {
		updates["next_run_at"] = &next
	}
	database.UpdateBackupSchedule(s.ID, updates)
}

func resolveScheduleInstances(instanceIDsJSON string) ([]uint, error) {
	if instanceIDsJSON == "ALL" {
		var instances []database.Instance
		if err := database.DB.Find(&instances).Error; err != nil {
			return nil, err
		}
		ids := make([]uint, len(instances))
		for i, inst := range instances {
			ids[i] = inst.ID
		}
		return ids, nil
	}

	var ids []uint
	if err := json.Unmarshal([]byte(instanceIDsJSON), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}
