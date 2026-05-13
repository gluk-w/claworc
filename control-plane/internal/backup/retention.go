package backup

import (
	"context"
	"log"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// runRetentionCleanup iterates all backup schedules with a retention policy
// set and deletes expired scheduled backups for each one.
func runRetentionCleanup(ctx context.Context) {
	schedules, err := database.ListBackupSchedules()
	if err != nil {
		log.Printf("backup retention: failed to list schedules: %v", err)
		return
	}
	for _, s := range schedules {
		if ctx.Err() != nil {
			return
		}
		cleanupExpiredForSchedule(ctx, s)
	}
}

// cleanupExpiredForSchedule deletes completed, scheduled backups older than
// the schedule's retention window. Manual (ad-hoc) backups are preserved:
// only backups with note="scheduled" are considered.
func cleanupExpiredForSchedule(ctx context.Context, s database.BackupSchedule) {
	if s.RetentionDays <= 0 {
		return
	}

	instanceIDs, err := resolveScheduleInstances(s)
	if err != nil {
		log.Printf("backup retention: schedule %d: failed to resolve instances: %v", s.ID, err)
		return
	}
	if len(instanceIDs) == 0 {
		return
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -s.RetentionDays)

	var expired []database.Backup
	if err := database.DB.Where(
		"instance_id IN ? AND note = ? AND status = ? AND created_at < ?",
		instanceIDs, "scheduled", "completed", cutoff,
	).Find(&expired).Error; err != nil {
		log.Printf("backup retention: schedule %d: query failed: %v", s.ID, err)
		return
	}

	for _, b := range expired {
		if ctx.Err() != nil {
			return
		}
		if err := DeleteBackup(b.ID); err != nil {
			log.Printf("backup retention: schedule %d: delete backup %d failed: %v", s.ID, b.ID, err)
		}
	}
}
