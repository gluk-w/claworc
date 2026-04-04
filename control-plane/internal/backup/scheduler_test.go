package backup

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupSchedulerTestDB(t *testing.T) {
	t.Helper()
	// Use a file-based DB in the test temp dir so that each test has a truly
	// isolated database even when backup goroutines from previous tests are
	// still referencing the global database.DB.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	var err error
	database.DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	database.DB.AutoMigrate(&database.Instance{}, &database.Setting{}, &database.Backup{}, &database.BackupSchedule{})
}

// waitForBackups polls the recording orchestrator until the expected number of
// backups have been started (or until a deadline).
func waitForBackups(orch *recordingOrch, expected int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		orch.mu.Lock()
		n := len(orch.backups)
		orch.mu.Unlock()
		if n >= expected || time.Now().After(deadline) {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForAllBackupsComplete waits until all Backup records in the DB have a
// non-running status (completed or failed).
func waitForAllBackupsComplete(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var running int64
		database.DB.Model(&database.Backup{}).Where("status = ?", "running").Count(&running)
		if running == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// --- ComputeNextRun ---

func TestComputeNextRun_Daily(t *testing.T) {
	next, err := ComputeNextRun("0 2 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next.Before(time.Now()) {
		t.Errorf("next run should be in the future, got %v", next)
	}
}

func TestComputeNextRun_InvalidExpression(t *testing.T) {
	_, err := ComputeNextRun("not a cron")
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestComputeNextRun_EveryMinute(t *testing.T) {
	next, err := ComputeNextRun("* * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	diff := time.Until(next)
	if diff < 0 || diff > 61*time.Second {
		t.Errorf("every-minute cron should fire within 61s, diff=%v", diff)
	}
}

// --- resolveScheduleInstances ---

func TestResolveScheduleInstances_ALL(t *testing.T) {
	setupSchedulerTestDB(t)

	database.DB.Create(&database.Instance{Name: "inst-1", DisplayName: "I1", Status: "running"})
	database.DB.Create(&database.Instance{Name: "inst-2", DisplayName: "I2", Status: "stopped"})

	ids, err := resolveScheduleInstances("ALL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 instances for ALL, got %d", len(ids))
	}
}

func TestResolveScheduleInstances_JSONArray(t *testing.T) {
	ids, err := resolveScheduleInstances("[1,3,5]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 ids, got %d", len(ids))
	}
	if ids[0] != 1 || ids[1] != 3 || ids[2] != 5 {
		t.Errorf("unexpected ids: %v", ids)
	}
}

func TestResolveScheduleInstances_InvalidJSON(t *testing.T) {
	_, err := resolveScheduleInstances("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestResolveScheduleInstances_EmptyDB(t *testing.T) {
	setupSchedulerTestDB(t)

	ids, err := resolveScheduleInstances("ALL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 instances from empty DB, got %d", len(ids))
	}
}

// --- executeSchedule ---

// recordingOrch captures backup creation calls.
type recordingOrch struct {
	mockOrch
	mu      sync.Mutex
	backups []string // instance names that were backed up
}

func (r *recordingOrch) StreamExecInInstance(_ context.Context, name string, _ []string, stdout io.Writer) (string, int, error) {
	r.mu.Lock()
	r.backups = append(r.backups, name)
	r.mu.Unlock()
	stdout.Write([]byte("data"))
	return "", 0, nil
}

func TestExecuteSchedule_SpecificInstances(t *testing.T) {
	setupSchedulerTestDB(t)
	config.Cfg.DataPath = t.TempDir()

	inst1 := database.Instance{Name: "bot-a", DisplayName: "A", Status: "running"}
	inst2 := database.Instance{Name: "bot-b", DisplayName: "B", Status: "running"}
	database.DB.Create(&inst1)
	database.DB.Create(&inst2)

	orch := &recordingOrch{}
	idsJSON, _ := json.Marshal([]uint{inst1.ID, inst2.ID})

	now := time.Now().UTC()
	sched := database.BackupSchedule{
		InstanceIDs:    string(idsJSON),
		CronExpression: "0 2 * * *",
		Paths:          `["HOME"]`,

		NextRunAt:      &now,
	}
	database.CreateBackupSchedule(&sched)

	executeSchedule(context.Background(), orch, sched)

	// Wait for async backup goroutines
	n := waitForBackups(orch, 2, 5*time.Second)
	if n != 2 {
		t.Errorf("expected 2 backup calls, got %d", n)
	}

	// Wait for goroutines to finish DB writes
	waitForAllBackupsComplete(t, 5*time.Second)

	// Verify schedule was updated
	updated, err := database.GetBackupSchedule(sched.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if updated.LastRunAt == nil {
		t.Error("expected last_run_at to be set")
	}
	if updated.NextRunAt == nil {
		t.Error("expected next_run_at to be recalculated")
	} else if !updated.NextRunAt.After(now) {
		t.Error("next_run_at should be after original time")
	}
}

func TestExecuteSchedule_ALL(t *testing.T) {
	setupSchedulerTestDB(t)
	config.Cfg.DataPath = t.TempDir()

	database.DB.Create(&database.Instance{Name: "bot-x", DisplayName: "X", Status: "running"})
	database.DB.Create(&database.Instance{Name: "bot-y", DisplayName: "Y", Status: "running"})
	database.DB.Create(&database.Instance{Name: "bot-z", DisplayName: "Z", Status: "running"})

	orch := &recordingOrch{}

	now := time.Now().UTC()
	sched := database.BackupSchedule{
		InstanceIDs:    "ALL",
		CronExpression: "0 2 * * *",
		Paths:          `["HOME"]`,

		NextRunAt:      &now,
	}
	database.CreateBackupSchedule(&sched)

	executeSchedule(context.Background(), orch, sched)

	n := waitForBackups(orch, 3, 5*time.Second)
	if n != 3 {
		t.Errorf("expected 3 backup calls for ALL, got %d", n)
	}

	waitForAllBackupsComplete(t, 5*time.Second)
}

func TestExecuteSchedule_MissingInstance(t *testing.T) {
	setupSchedulerTestDB(t)
	config.Cfg.DataPath = t.TempDir()

	inst := database.Instance{Name: "bot-real", DisplayName: "Real", Status: "running"}
	database.DB.Create(&inst)

	orch := &recordingOrch{}
	idsJSON, _ := json.Marshal([]uint{inst.ID, 999})

	now := time.Now().UTC()
	sched := database.BackupSchedule{
		InstanceIDs:    string(idsJSON),
		CronExpression: "0 2 * * *",
		Paths:          `["/"]`,

		NextRunAt:      &now,
	}
	database.CreateBackupSchedule(&sched)

	// Should not panic — missing instance is skipped
	executeSchedule(context.Background(), orch, sched)

	n := waitForBackups(orch, 1, 5*time.Second)
	if n != 1 {
		t.Errorf("expected 1 backup (skipping missing), got %d", n)
	}

	waitForAllBackupsComplete(t, 5*time.Second)
}

func TestExecuteSchedule_CustomPaths(t *testing.T) {
	setupSchedulerTestDB(t)
	config.Cfg.DataPath = t.TempDir()

	inst := database.Instance{Name: "bot-paths", DisplayName: "Paths", Status: "running"}
	database.DB.Create(&inst)

	orch := &recordingOrch{}
	idsJSON, _ := json.Marshal([]uint{inst.ID})

	now := time.Now().UTC()
	sched := database.BackupSchedule{
		InstanceIDs:    string(idsJSON),
		CronExpression: "0 2 * * *",
		Paths:          `["HOME", "Homebrew"]`,

		NextRunAt:      &now,
	}
	database.CreateBackupSchedule(&sched)

	executeSchedule(context.Background(), orch, sched)

	waitForBackups(orch, 1, 5*time.Second)
	waitForAllBackupsComplete(t, 5*time.Second)

	// Verify the backup record has the paths stored
	var backups []database.Backup
	database.DB.Find(&backups)
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}
	var paths []string
	json.Unmarshal([]byte(backups[0].Paths), &paths)
	if len(paths) != 2 || paths[0] != "HOME" || paths[1] != "Homebrew" {
		t.Errorf("expected paths [HOME, Homebrew], got %v", paths)
	}
}

// --- ListDueSchedules ---

func TestListDueSchedules_FiltersCorrectly(t *testing.T) {
	setupSchedulerTestDB(t)

	past := time.Now().Add(-1 * time.Hour).UTC()
	future := time.Now().Add(1 * time.Hour).UTC()

	// Due schedule (past NextRunAt)
	database.CreateBackupSchedule(&database.BackupSchedule{
		InstanceIDs:    "ALL",
		CronExpression: "0 2 * * *",
		Paths:          `["HOME"]`,
		NextRunAt:      &past,
	})

	// Not due yet (future NextRunAt)
	database.CreateBackupSchedule(&database.BackupSchedule{
		InstanceIDs:    "ALL",
		CronExpression: "0 2 * * *",
		Paths:          `["HOME"]`,
		NextRunAt:      &future,
	})

	// No NextRunAt
	database.CreateBackupSchedule(&database.BackupSchedule{
		InstanceIDs:    "ALL",
		CronExpression: "0 2 * * *",
		Paths:          `["HOME"]`,
	})

	schedules, err := database.ListDueSchedules()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schedules) != 1 {
		t.Errorf("expected 1 due schedule, got %d", len(schedules))
	}
}

// --- StartScheduleExecutor ---

func TestStartScheduleExecutor_CancelStops(t *testing.T) {
	cancel := StartScheduleExecutor(context.Background())
	cancel()
}

// --- executeDueSchedules with no orchestrator ---

func TestExecuteDueSchedules_NoOrchestrator(t *testing.T) {
	setupSchedulerTestDB(t)

	orchestrator.Set(nil)

	past := time.Now().Add(-1 * time.Hour).UTC()
	database.CreateBackupSchedule(&database.BackupSchedule{
		InstanceIDs:    "ALL",
		CronExpression: "0 2 * * *",
		Paths:          `["HOME"]`,

		NextRunAt:      &past,
	})

	// Should not panic when orchestrator is nil
	executeDueSchedules(context.Background())
}
