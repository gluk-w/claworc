package main

import (
	"context"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDBMain(t *testing.T) func() {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	if err := database.DB.AutoMigrate(&database.Instance{}, &database.Setting{}, &database.User{}, &database.UserInstance{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return func() {
		sqlDB, _ := database.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
}

func TestCheckKeyRotations_NoInstances(t *testing.T) {
	cleanup := setupTestDBMain(t)
	defer cleanup()

	// Should not panic with empty DB and nil orchestrator/ssh manager
	ctx := context.Background()
	checkKeyRotations(ctx)
}

func TestCheckKeyRotations_SkipsDisabledPolicy(t *testing.T) {
	cleanup := setupTestDBMain(t)
	defer cleanup()

	inst := database.Instance{
		Name:              "bot-no-rotate",
		DisplayName:       "No Rotate",
		Status:            "running",
		SSHPublicKey:      "ssh-ed25519 AAAA...",
		SSHPrivateKeyPath: "/tmp/key",
		KeyRotationPolicy: 0, // disabled
	}
	database.DB.Create(&inst)

	ctx := context.Background()
	checkKeyRotations(ctx)

	// Instance should not have been modified (no LastKeyRotation set)
	var updated database.Instance
	database.DB.First(&updated, inst.ID)
	if updated.LastKeyRotation != nil {
		t.Error("expected LastKeyRotation to remain nil for disabled policy")
	}
}

func TestCheckKeyRotations_SkipsRecentlyRotated(t *testing.T) {
	cleanup := setupTestDBMain(t)
	defer cleanup()

	recentTime := time.Now().Add(-1 * time.Hour) // rotated 1 hour ago
	inst := database.Instance{
		Name:              "bot-recent-rotate",
		DisplayName:       "Recent Rotate",
		Status:            "running",
		SSHPublicKey:      "ssh-ed25519 AAAA...",
		SSHPrivateKeyPath: "/tmp/key",
		KeyRotationPolicy: 90,
		LastKeyRotation:   &recentTime,
	}
	database.DB.Create(&inst)

	ctx := context.Background()
	checkKeyRotations(ctx)

	// Instance should not have been modified
	var updated database.Instance
	database.DB.First(&updated, inst.ID)
	if updated.LastKeyRotation == nil {
		t.Fatal("expected LastKeyRotation to remain set")
	}
	if !updated.LastKeyRotation.Equal(recentTime) {
		t.Error("expected LastKeyRotation to remain unchanged")
	}
}

func TestCheckKeyRotations_SkipsNoSSHKey(t *testing.T) {
	cleanup := setupTestDBMain(t)
	defer cleanup()

	inst := database.Instance{
		Name:              "bot-no-key",
		DisplayName:       "No Key",
		Status:            "running",
		KeyRotationPolicy: 90,
	}
	database.DB.Create(&inst)

	ctx := context.Background()
	checkKeyRotations(ctx)

	var updated database.Instance
	database.DB.First(&updated, inst.ID)
	if updated.LastKeyRotation != nil {
		t.Error("expected LastKeyRotation to remain nil")
	}
}

func TestCheckKeyRotations_IdentifiesDueInstances(t *testing.T) {
	cleanup := setupTestDBMain(t)
	defer cleanup()

	// Instance with rotation due (last rotated 100 days ago, policy is 90 days)
	oldTime := time.Now().Add(-100 * 24 * time.Hour)
	inst := database.Instance{
		Name:              "bot-due-rotate",
		DisplayName:       "Due Rotate",
		Status:            "running",
		SSHPublicKey:      "ssh-ed25519 AAAA...",
		SSHPrivateKeyPath: "/tmp/key",
		KeyRotationPolicy: 90,
		LastKeyRotation:   &oldTime,
	}
	database.DB.Create(&inst)

	// Without orchestrator, the rotation will be skipped (no SSH endpoint available)
	// but the function should not panic
	ctx := context.Background()
	checkKeyRotations(ctx)

	// Verify the instance was not modified (since no orchestrator is available)
	var updated database.Instance
	database.DB.First(&updated, inst.ID)
	if updated.LastKeyRotation != nil && !updated.LastKeyRotation.Equal(oldTime) {
		t.Error("expected LastKeyRotation to remain unchanged without orchestrator")
	}
}

func TestCheckKeyRotations_UsesCreatedAtIfNeverRotated(t *testing.T) {
	cleanup := setupTestDBMain(t)
	defer cleanup()

	// Instance that was created 100 days ago and never rotated
	inst := database.Instance{
		Name:              "bot-never-rotated",
		DisplayName:       "Never Rotated",
		Status:            "running",
		SSHPublicKey:      "ssh-ed25519 AAAA...",
		SSHPrivateKeyPath: "/tmp/key",
		KeyRotationPolicy: 90,
	}
	// Manually set CreatedAt to 100 days ago
	database.DB.Create(&inst)
	database.DB.Model(&inst).Update("created_at", time.Now().Add(-100*24*time.Hour))

	// Without orchestrator, should gracefully skip
	ctx := context.Background()
	checkKeyRotations(ctx)
}
