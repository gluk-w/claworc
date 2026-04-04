package handlers

import (
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/backup"
)

func TestComputeNextRun_Valid(t *testing.T) {
	next, err := backup.ComputeNextRun("0 2 * * *") // daily at 2am
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next.Before(time.Now()) {
		t.Errorf("next run should be in the future, got %v", next)
	}
	if next.Hour() != 2 || next.Minute() != 0 {
		t.Errorf("expected 02:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

func TestComputeNextRun_Invalid(t *testing.T) {
	_, err := backup.ComputeNextRun("invalid")
	if err == nil {
		t.Fatal("expected error for invalid cron expression")
	}
}

func TestNextRunAtRecalculated_OnCreate(t *testing.T) {
	// Verify that ComputeNextRun returns different times for different cron expressions
	next1, err := backup.ComputeNextRun("0 2 * * *") // daily at 2am
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	next2, err := backup.ComputeNextRun("0 14 * * *") // daily at 2pm
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if next1.Equal(next2) {
		t.Error("different cron expressions should produce different next run times")
	}
}

func TestNextRunAtRecalculated_OnUpdate(t *testing.T) {
	// Simulate the update flow: changing cron expression must produce a new NextRunAt
	originalCron := "0 2 * * *"   // daily at 2am
	updatedCron := "30 3 * * 1"   // weekly Monday at 3:30am

	originalNext, err := backup.ComputeNextRun(originalCron)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updatedNext, err := backup.ComputeNextRun(updatedCron)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if originalNext.Equal(updatedNext) {
		t.Error("changing cron expression should change the next run time")
	}

	// Both should be in the future
	now := time.Now()
	if originalNext.Before(now) {
		t.Errorf("original next run should be in the future, got %v", originalNext)
	}
	if updatedNext.Before(now) {
		t.Errorf("updated next run should be in the future, got %v", updatedNext)
	}
}

func TestComputeNextRun_CommonPresets(t *testing.T) {
	cases := []struct {
		name string
		cron string
	}{
		{"daily at 2am", "0 2 * * *"},
		{"weekly Sunday at 2am", "0 2 * * 0"},
		{"monthly 1st at 2am", "0 2 1 * *"},
		{"every 6 hours", "0 */6 * * *"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, err := backup.ComputeNextRun(tc.cron)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.cron, err)
			}
			if next.Before(time.Now()) {
				t.Errorf("next run should be in the future for %q, got %v", tc.cron, next)
			}
		})
	}
}
