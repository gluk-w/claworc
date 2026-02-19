package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gluk-w/claworc/llm-proxy/internal/config"
)

// SetupTestDB initializes a test database.
func SetupTestDB(t *testing.T) func() {
	t.Helper()

	// Create temp dir for test DB
	tmpDir, err := os.MkdirTemp("", "llm-proxy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Set config to use temp DB
	config.Cfg.DatabasePath = filepath.Join(tmpDir, "test.db")

	// Initialize database
	if err := Init(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to init database: %v", err)
	}

	// Return cleanup function
	return func() {
		Close()
		os.RemoveAll(tmpDir)
	}
}

func TestDatabaseInit(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Verify tables were created
	var count int64

	DB.Model(&InstanceToken{}).Count(&count)
	DB.Model(&ProviderKey{}).Count(&count)
	DB.Model(&UsageRecord{}).Count(&count)
	DB.Model(&BudgetLimit{}).Count(&count)
	DB.Model(&RateLimit{}).Count(&count)

	// Verify pricing was seeded
	DB.Model(&ModelPricing{}).Count(&count)
	if count == 0 {
		t.Error("Model pricing not seeded")
	}
}

func TestInstanceTokenCRUD(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	token := InstanceToken{
		InstanceName: "bot-test",
		Token:        "test-token-123",
		Enabled:      true,
	}

	// Create
	if err := DB.Create(&token).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Read
	var fetched InstanceToken
	if err := DB.Where("instance_name = ?", "bot-test").First(&fetched).Error; err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if fetched.Token != "test-token-123" {
		t.Errorf("Token = %s, want test-token-123", fetched.Token)
	}

	// Update
	DB.Model(&fetched).Update("enabled", false)
	DB.First(&fetched, fetched.ID)
	if fetched.Enabled {
		t.Error("Token should be disabled")
	}

	// Delete
	DB.Delete(&fetched)
	var count int64
	DB.Model(&InstanceToken{}).Where("instance_name = ?", "bot-test").Count(&count)
	if count != 0 {
		t.Error("Token should be deleted")
	}
}

func TestProviderKeyUniqueness(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	key1 := ProviderKey{
		ProviderName: "anthropic",
		Scope:        "global",
		KeyValue:     "sk-ant-123",
	}
	if err := DB.Create(&key1).Error; err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Attempt duplicate (same provider + scope) should fail
	key2 := ProviderKey{
		ProviderName: "anthropic",
		Scope:        "global",
		KeyValue:     "sk-ant-456",
	}
	if err := DB.Create(&key2).Error; err == nil {
		t.Error("Expected unique constraint violation")
	}

	// Different scope should succeed
	key3 := ProviderKey{
		ProviderName: "anthropic",
		Scope:        "bot-test",
		KeyValue:     "sk-ant-789",
	}
	if err := DB.Create(&key3).Error; err != nil {
		t.Errorf("Create with different scope failed: %v", err)
	}
}
