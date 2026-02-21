package database

import (
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&Instance{}, &Setting{}, &InstanceAPIKey{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

func TestInstanceSSHFieldsMigration(t *testing.T) {
	db := setupTestDB(t)

	// Verify the columns exist by checking the table schema
	var columns []struct {
		Name string `gorm:"column:name"`
	}
	db.Raw("PRAGMA table_info(instances)").Scan(&columns)

	colNames := make(map[string]bool)
	for _, c := range columns {
		colNames[c.Name] = true
	}

	for _, expected := range []string{"ssh_public_key", "ssh_private_key_path", "ssh_port"} {
		if !colNames[expected] {
			t.Errorf("expected column %q in instances table, found columns: %v", expected, colNames)
		}
	}
}

func TestInstanceSSHPortDefault(t *testing.T) {
	db := setupTestDB(t)

	inst := Instance{
		Name:        "bot-test",
		DisplayName: "Test Instance",
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	var loaded Instance
	if err := db.First(&loaded, inst.ID).Error; err != nil {
		t.Fatalf("failed to load instance: %v", err)
	}

	if loaded.SSHPort != 22 {
		t.Errorf("expected SSHPort default 22, got %d", loaded.SSHPort)
	}
}

func TestInstanceSSHFieldsRoundTrip(t *testing.T) {
	db := setupTestDB(t)

	inst := Instance{
		Name:              "bot-ssh-test",
		DisplayName:       "SSH Test",
		SSHPublicKey:      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest claworc@control-plane",
		SSHPrivateKeyPath: "/app/data/ssh-keys/bot-ssh-test.key",
		SSHPort:           2222,
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	var loaded Instance
	if err := db.First(&loaded, inst.ID).Error; err != nil {
		t.Fatalf("failed to load instance: %v", err)
	}

	if loaded.SSHPublicKey != inst.SSHPublicKey {
		t.Errorf("SSHPublicKey mismatch: got %q, want %q", loaded.SSHPublicKey, inst.SSHPublicKey)
	}
	if loaded.SSHPrivateKeyPath != inst.SSHPrivateKeyPath {
		t.Errorf("SSHPrivateKeyPath mismatch: got %q, want %q", loaded.SSHPrivateKeyPath, inst.SSHPrivateKeyPath)
	}
	if loaded.SSHPort != inst.SSHPort {
		t.Errorf("SSHPort mismatch: got %d, want %d", loaded.SSHPort, inst.SSHPort)
	}
}

func TestInstanceSSHFieldsNotInJSON(t *testing.T) {
	inst := Instance{
		Name:              "bot-json-test",
		DisplayName:       "JSON Test",
		SSHPublicKey:      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAISecret",
		SSHPrivateKeyPath: "/app/data/ssh-keys/secret.key",
		SSHPort:           22,
	}

	data, err := json.Marshal(inst)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// SSHPublicKey and SSHPrivateKeyPath should be hidden (json:"-")
	if _, ok := m["ssh_public_key"]; ok {
		t.Error("SSHPublicKey should not appear in JSON output")
	}
	if _, ok := m["SSHPublicKey"]; ok {
		t.Error("SSHPublicKey should not appear in JSON output")
	}
	if _, ok := m["ssh_private_key_path"]; ok {
		t.Error("SSHPrivateKeyPath should not appear in JSON output")
	}
	if _, ok := m["SSHPrivateKeyPath"]; ok {
		t.Error("SSHPrivateKeyPath should not appear in JSON output")
	}

	// SSHPort should be visible
	if _, ok := m["ssh_port"]; !ok {
		t.Error("SSHPort should appear in JSON output as ssh_port")
	}
}

func TestInstanceSSHFieldsUpdate(t *testing.T) {
	db := setupTestDB(t)

	inst := Instance{
		Name:        "bot-update-test",
		DisplayName: "Update Test",
	}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	// Update SSH fields
	if err := db.Model(&inst).Updates(map[string]interface{}{
		"ssh_public_key":      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUpdated",
		"ssh_private_key_path": "/app/data/ssh-keys/updated.key",
		"ssh_port":            2222,
	}).Error; err != nil {
		t.Fatalf("failed to update: %v", err)
	}

	var loaded Instance
	if err := db.First(&loaded, inst.ID).Error; err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if loaded.SSHPublicKey != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUpdated" {
		t.Errorf("SSHPublicKey not updated: got %q", loaded.SSHPublicKey)
	}
	if loaded.SSHPrivateKeyPath != "/app/data/ssh-keys/updated.key" {
		t.Errorf("SSHPrivateKeyPath not updated: got %q", loaded.SSHPrivateKeyPath)
	}
	if loaded.SSHPort != 2222 {
		t.Errorf("SSHPort not updated: got %d", loaded.SSHPort)
	}
}
