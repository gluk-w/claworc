package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init() error {
	dataDir := config.Cfg.DataPath
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}
	}
	dbPath := filepath.Join(dataDir, "claworc.db")

	var err error
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	// Use busy_timeout instead of WAL journal mode. WAL uses mmap() for the
	// shared-memory file (.db-shm), which macOS Docker Desktop bind mounts
	// (VirtioFS/gRPC FUSE) do not support properly, causing SQLITE_IOERR on
	// every query. The default DELETE journal mode avoids mmap entirely.
	// busy_timeout handles write contention that WAL would otherwise reduce.
	if _, err := sqlDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("set busy timeout: %w", err)
	}

	if err := DB.AutoMigrate(&Instance{}, &Setting{}, &User{}, &UserInstance{}, &WebAuthnCredential{}, &LLMProvider{}, &LLMGatewayKey{}, &Skill{}, &Backup{}, &BackupSchedule{}, &SharedFolder{}); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	if err := seedDefaults(); err != nil {
		return fmt.Errorf("seed defaults: %w", err)
	}

	migrateProviderAPIKeys()

	return nil
}

// migrateProviderAPIKeys moves API keys from the settings table into the
// LLMProvider.APIKey column. Handles two legacy formats:
//   - api_key:provider:<id>  (previous migration format)
//   - api_key:<KEY>_API_KEY  (original format)
//
// Idempotent: providers that already have APIKey populated are skipped.
func migrateProviderAPIKeys() {
	var providers []LLMProvider
	DB.Find(&providers)
	for _, p := range providers {
		if p.APIKey != "" {
			continue // already migrated
		}

		// Try the newer settings format first: api_key:provider:<id>
		settingKey := fmt.Sprintf("api_key:provider:%d", p.ID)
		if val, err := GetSetting(settingKey); err == nil && val != "" {
			if err := DB.Model(&p).Update("api_key", val).Error; err != nil {
				log.Printf("migrate provider API key (provider:%d): %v", p.ID, err)
				continue
			}
			DeleteSetting(settingKey)
			log.Printf("Migrated provider API key: setting %s → LLMProvider.APIKey (id=%d)", settingKey, p.ID)
			continue
		}

		// Try the legacy format: api_key:<KEY>_API_KEY (global providers only)
		if p.InstanceID != nil {
			continue
		}
		oldKey := "api_key:" + strings.ReplaceAll(strings.ToUpper(p.Key), "-", "_") + "_API_KEY"
		if val, err := GetSetting(oldKey); err == nil && val != "" {
			if err := DB.Model(&p).Update("api_key", val).Error; err != nil {
				log.Printf("migrate provider API key %s → LLMProvider.APIKey (id=%d): %v", oldKey, p.ID, err)
				continue
			}
			DeleteSetting(oldKey)
			log.Printf("Migrated provider API key: setting %s → LLMProvider.APIKey (id=%d)", oldKey, p.ID)
		}
	}
}

func seedDefaults() error {
	defaults := map[string]string{
		"default_cpu_request":          "500m",
		"default_cpu_limit":            "2000m",
		"default_memory_request":       "1Gi",
		"default_memory_limit":         "4Gi",
		"default_storage_homebrew":     "10Gi",
		"default_storage_home":         "10Gi",
		"default_container_image":      "glukw/openclaw-vnc-chromium:latest",
		"default_vnc_resolution":       "1920x1080",
		"orchestrator_backend":         "auto",
		"default_models":               "[]",
		"ssh_key_rotation_policy_days": "90",
		"ssh_audit_retention_days":     "90",
		"default_timezone":             "America/New_York",
		"default_user_agent":           "",
	}

	for key, value := range defaults {
		var count int64
		DB.Model(&Setting{}).Where("key = ?", key).Count(&count)
		if count == 0 {
			if err := DB.Create(&Setting{Key: key, Value: value}).Error; err != nil {
				return fmt.Errorf("seed setting %s: %w", key, err)
			}
		}
	}

	return nil
}

func Close() error {
	if DB != nil {
		sqlDB, err := DB.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

func GetSetting(key string) (string, error) {
	var s Setting
	if err := DB.Where("key = ?", key).First(&s).Error; err != nil {
		return "", err
	}
	return s.Value, nil
}

func SetSetting(key, value string) error {
	return DB.Exec(
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) "+
			"ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
		key, value, time.Now().UTC(),
	).Error
}

func DeleteSetting(key string) error {
	return DB.Where("key = ?", key).Delete(&Setting{}).Error
}

// User helpers

func GetUserByUsername(username string) (*User, error) {
	var u User
	if err := DB.Where("username = ?", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func GetUserByID(id uint) (*User, error) {
	var u User
	if err := DB.First(&u, id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func CreateUser(user *User) error {
	return DB.Create(user).Error
}

func DeleteUser(id uint) error {
	DB.Where("user_id = ?", id).Delete(&UserInstance{})
	DB.Where("user_id = ?", id).Delete(&WebAuthnCredential{})
	return DB.Delete(&User{}, id).Error
}

func UpdateUserPassword(id uint, hash string) error {
	return DB.Model(&User{}).Where("id = ?", id).Update("password_hash", hash).Error
}

func ListUsers() ([]User, error) {
	var users []User
	if err := DB.Order("id").Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func UserCount() (int64, error) {
	var count int64
	err := DB.Model(&User{}).Count(&count).Error
	return count, err
}

func GetFirstAdmin() (*User, error) {
	var u User
	if err := DB.Where("role = ?", "admin").Order("id").First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// Instance assignment helpers

func GetUserInstances(userID uint) ([]uint, error) {
	var assignments []UserInstance
	if err := DB.Where("user_id = ?", userID).Find(&assignments).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, len(assignments))
	for i, a := range assignments {
		ids[i] = a.InstanceID
	}
	return ids, nil
}

func SetUserInstances(userID uint, instanceIDs []uint) error {
	DB.Where("user_id = ?", userID).Delete(&UserInstance{})
	for _, iid := range instanceIDs {
		if err := DB.Create(&UserInstance{UserID: userID, InstanceID: iid}).Error; err != nil {
			return err
		}
	}
	return nil
}

func IsUserAssignedToInstance(userID, instanceID uint) bool {
	var count int64
	DB.Model(&UserInstance{}).Where("user_id = ? AND instance_id = ?", userID, instanceID).Count(&count)
	return count > 0
}

// WebAuthn credential helpers

func GetWebAuthnCredentials(userID uint) ([]WebAuthnCredential, error) {
	var creds []WebAuthnCredential
	if err := DB.Where("user_id = ?", userID).Find(&creds).Error; err != nil {
		return nil, err
	}
	return creds, nil
}

func SaveWebAuthnCredential(cred *WebAuthnCredential) error {
	return DB.Create(cred).Error
}

func DeleteWebAuthnCredential(id string, userID uint) error {
	return DB.Where("id = ? AND user_id = ?", id, userID).Delete(&WebAuthnCredential{}).Error
}

func UpdateCredentialSignCount(id string, count uint32) error {
	return DB.Model(&WebAuthnCredential{}).Where("id = ?", id).Update("sign_count", count).Error
}

// Backup helpers

func CreateBackup(b *Backup) error {
	return DB.Create(b).Error
}

func GetBackup(id uint) (*Backup, error) {
	var b Backup
	if err := DB.First(&b, id).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

func ListBackups(instanceID uint) ([]Backup, error) {
	var backups []Backup
	if err := DB.Where("instance_id = ?", instanceID).Order("created_at DESC").Find(&backups).Error; err != nil {
		return nil, err
	}
	return backups, nil
}

func ListAllBackups() ([]Backup, error) {
	var backups []Backup
	if err := DB.Order("created_at DESC").Find(&backups).Error; err != nil {
		return nil, err
	}
	return backups, nil
}

func UpdateBackup(id uint, updates map[string]interface{}) error {
	return DB.Model(&Backup{}).Where("id = ?", id).Updates(updates).Error
}

func DeleteBackupRecord(id uint) error {
	return DB.Delete(&Backup{}, id).Error
}

func GetLatestCompletedBackup(instanceID uint) (*Backup, error) {
	var b Backup
	if err := DB.Where("instance_id = ? AND status = ?", instanceID, "completed").
		Order("created_at DESC").First(&b).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

// BackupSchedule helpers

func CreateBackupSchedule(s *BackupSchedule) error {
	return DB.Create(s).Error
}

func GetBackupSchedule(id uint) (*BackupSchedule, error) {
	var s BackupSchedule
	if err := DB.First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func ListBackupSchedules() ([]BackupSchedule, error) {
	var schedules []BackupSchedule
	if err := DB.Order("created_at DESC").Find(&schedules).Error; err != nil {
		return nil, err
	}
	return schedules, nil
}

func UpdateBackupSchedule(id uint, updates map[string]interface{}) error {
	return DB.Model(&BackupSchedule{}).Where("id = ?", id).Updates(updates).Error
}

func DeleteBackupSchedule(id uint) error {
	return DB.Delete(&BackupSchedule{}, id).Error
}

// Shared Folder helpers

func CreateSharedFolder(sf *SharedFolder) error {
	return DB.Create(sf).Error
}

func GetSharedFolder(id uint) (*SharedFolder, error) {
	var sf SharedFolder
	if err := DB.First(&sf, id).Error; err != nil {
		return nil, err
	}
	return &sf, nil
}

func ListSharedFolders(ownerID uint, isAdmin bool) ([]SharedFolder, error) {
	var folders []SharedFolder
	q := DB.Order("created_at DESC")
	if !isAdmin {
		q = q.Where("owner_id = ?", ownerID)
	}
	if err := q.Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

func UpdateSharedFolder(id uint, updates map[string]interface{}) error {
	return DB.Model(&SharedFolder{}).Where("id = ?", id).Updates(updates).Error
}

func DeleteSharedFolder(id uint) error {
	return DB.Delete(&SharedFolder{}, id).Error
}

// GetSharedFoldersForInstance returns all shared folders that include the given instance ID.
func GetSharedFoldersForInstance(instanceID uint) ([]SharedFolder, error) {
	var all []SharedFolder
	if err := DB.Find(&all).Error; err != nil {
		return nil, err
	}
	var result []SharedFolder
	for _, sf := range all {
		for _, id := range ParseSharedFolderInstanceIDs(sf.InstanceIDs) {
			if id == instanceID {
				result = append(result, sf)
				break
			}
		}
	}
	return result, nil
}

func ListDueSchedules() ([]BackupSchedule, error) {
	var schedules []BackupSchedule
	if err := DB.Where("next_run_at IS NOT NULL AND next_run_at <= ?", time.Now().UTC()).
		Find(&schedules).Error; err != nil {
		return nil, err
	}
	return schedules, nil
}
