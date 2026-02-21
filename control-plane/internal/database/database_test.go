package database

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB creates an in-memory SQLite database for testing.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&Instance{}, &Setting{}, &InstanceAPIKey{}, &User{}, &UserInstance{}, &WebAuthnCredential{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return db
}

func TestInstanceSSHFieldsExist(t *testing.T) {
	db := setupTestDB(t)

	inst := Instance{
		Name:        "bot-test",
		DisplayName: "Test",
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	var loaded Instance
	if err := db.First(&loaded, inst.ID).Error; err != nil {
		t.Fatalf("load instance: %v", err)
	}

	// Verify default values
	if loaded.SSHPort != 22 {
		t.Errorf("expected SSHPort default 22, got %d", loaded.SSHPort)
	}
	if loaded.SSHPublicKey != "" {
		t.Errorf("expected SSHPublicKey empty, got %q", loaded.SSHPublicKey)
	}
	if loaded.SSHPrivateKeyPath != "" {
		t.Errorf("expected SSHPrivateKeyPath empty, got %q", loaded.SSHPrivateKeyPath)
	}
}

func TestInstanceSSHFieldsRoundTrip(t *testing.T) {
	db := setupTestDB(t)

	pubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTesting claworc"
	keyPath := "/app/data/ssh-keys/bot-test.key"
	sshPort := 2222

	inst := Instance{
		Name:              "bot-test",
		DisplayName:       "Test",
		SSHPublicKey:      pubKey,
		SSHPrivateKeyPath: keyPath,
		SSHPort:           sshPort,
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	var loaded Instance
	if err := db.First(&loaded, inst.ID).Error; err != nil {
		t.Fatalf("load instance: %v", err)
	}

	if loaded.SSHPublicKey != pubKey {
		t.Errorf("SSHPublicKey = %q, want %q", loaded.SSHPublicKey, pubKey)
	}
	if loaded.SSHPrivateKeyPath != keyPath {
		t.Errorf("SSHPrivateKeyPath = %q, want %q", loaded.SSHPrivateKeyPath, keyPath)
	}
	if loaded.SSHPort != sshPort {
		t.Errorf("SSHPort = %d, want %d", loaded.SSHPort, sshPort)
	}
}

func TestInstanceSSHFieldsUpdate(t *testing.T) {
	db := setupTestDB(t)

	inst := Instance{
		Name:        "bot-update",
		DisplayName: "Update Test",
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	newPubKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINewKeyForUpdate claworc"
	newKeyPath := "/app/data/ssh-keys/bot-update.key"

	if err := db.Model(&inst).Updates(map[string]interface{}{
		"ssh_public_key":      newPubKey,
		"ssh_private_key_path": newKeyPath,
		"ssh_port":            2222,
	}).Error; err != nil {
		t.Fatalf("update instance: %v", err)
	}

	var loaded Instance
	if err := db.First(&loaded, inst.ID).Error; err != nil {
		t.Fatalf("load instance: %v", err)
	}

	if loaded.SSHPublicKey != newPubKey {
		t.Errorf("SSHPublicKey = %q, want %q", loaded.SSHPublicKey, newPubKey)
	}
	if loaded.SSHPrivateKeyPath != newKeyPath {
		t.Errorf("SSHPrivateKeyPath = %q, want %q", loaded.SSHPrivateKeyPath, newKeyPath)
	}
	if loaded.SSHPort != 2222 {
		t.Errorf("SSHPort = %d, want 2222", loaded.SSHPort)
	}
}

func TestInstanceSSHFieldsMigrationOnExistingDB(t *testing.T) {
	// Simulate migrating an existing database: create a DB with a table
	// that lacks the SSH columns, then run AutoMigrate and verify the
	// columns are added.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Phase 1: create table WITHOUT SSH columns using raw SQL.
	db1, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db phase 1: %v", err)
	}
	sqlDB1, _ := db1.DB()
	_, err = sqlDB1.Exec(`CREATE TABLE instances (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		display_name TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'creating',
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME
	)`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	_, err = sqlDB1.Exec(`INSERT INTO instances (name, display_name, status) VALUES ('bot-legacy', 'Legacy', 'running')`)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	sqlDB1.Close()

	// Phase 2: open with GORM and run AutoMigrate (should add SSH columns).
	db2, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db phase 2: %v", err)
	}
	if err := db2.AutoMigrate(&Instance{}); err != nil {
		t.Fatalf("auto-migrate phase 2: %v", err)
	}

	var loaded Instance
	if err := db2.Where("name = ?", "bot-legacy").First(&loaded).Error; err != nil {
		t.Fatalf("load legacy instance: %v", err)
	}

	// Legacy row should have zero-value defaults for SSH fields.
	if loaded.SSHPublicKey != "" {
		t.Errorf("expected SSHPublicKey empty for legacy row, got %q", loaded.SSHPublicKey)
	}
	if loaded.SSHPrivateKeyPath != "" {
		t.Errorf("expected SSHPrivateKeyPath empty for legacy row, got %q", loaded.SSHPrivateKeyPath)
	}
	// SQLite doesn't enforce default for ALTER ADD COLUMN the same way,
	// so the default may be 0 for integer columns on legacy rows.
	if loaded.SSHPort != 0 && loaded.SSHPort != 22 {
		t.Errorf("expected SSHPort 0 or 22 for legacy row, got %d", loaded.SSHPort)
	}

	// Verify new rows get the default.
	newInst := Instance{
		Name:        "bot-new",
		DisplayName: "New",
	}
	if err := db2.Create(&newInst).Error; err != nil {
		t.Fatalf("create new instance: %v", err)
	}
	var newLoaded Instance
	if err := db2.First(&newLoaded, newInst.ID).Error; err != nil {
		t.Fatalf("load new instance: %v", err)
	}
	if newLoaded.SSHPort != 22 {
		t.Errorf("expected SSHPort 22 for new row, got %d", newLoaded.SSHPort)
	}

	sqlDB2, _ := db2.DB()
	sqlDB2.Close()
	os.Remove(dbPath)
}

func TestInstanceSSHFieldsNotInJSON(t *testing.T) {
	// Verify that SSHPublicKey and SSHPrivateKeyPath are excluded from
	// JSON serialization (json:"-" tags prevent leaking secrets in API responses).
	inst := Instance{
		Name:              "bot-json",
		DisplayName:       "JSON Test",
		SSHPublicKey:      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey",
		SSHPrivateKeyPath: "/app/data/ssh-keys/bot-json.key",
		SSHPort:           22,
	}

	data, err := json.Marshal(inst)
	if err != nil {
		t.Fatalf("marshal instance: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["SSHPublicKey"]; ok {
		t.Error("SSHPublicKey should not appear in JSON output")
	}
	if _, ok := m["ssh_public_key"]; ok {
		t.Error("ssh_public_key should not appear in JSON output")
	}
	if _, ok := m["SSHPrivateKeyPath"]; ok {
		t.Error("SSHPrivateKeyPath should not appear in JSON output")
	}
	if _, ok := m["ssh_private_key_path"]; ok {
		t.Error("ssh_private_key_path should not appear in JSON output")
	}

	// ssh_port should be present since it has a json tag.
	if _, ok := m["ssh_port"]; !ok {
		t.Error("ssh_port should appear in JSON output")
	}
}
