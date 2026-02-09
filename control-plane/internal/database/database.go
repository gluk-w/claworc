package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/glukw/claworc/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Init() error {
	dbPath := config.Cfg.DatabasePath
	dbDir := filepath.Dir(dbPath)
	if dbDir != "" {
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			return fmt.Errorf("create db directory: %w", err)
		}
	}

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
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}

	if err := DB.AutoMigrate(&Instance{}, &Setting{}, &InstanceAPIKey{}, &User{}, &UserInstance{}, &WebAuthnCredential{}); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	if err := seedDefaults(); err != nil {
		return fmt.Errorf("seed defaults: %w", err)
	}

	if err := migrateAPIKeys(); err != nil {
		return fmt.Errorf("migrate api keys: %w", err)
	}

	if err := migrateSortOrder(); err != nil {
		return fmt.Errorf("migrate sort order: %w", err)
	}

	return nil
}

func seedDefaults() error {
	defaults := map[string]string{
		"default_cpu_request":      "500m",
		"default_cpu_limit":        "2000m",
		"default_memory_request":   "1Gi",
		"default_memory_limit":     "4Gi",
		"default_storage_homebrew": "10Gi",
		"default_storage_clawd":    "5Gi",
		"default_storage_chrome":   "5Gi",
		"anthropic_api_key":        "",
		"openai_api_key":           "",
		"brave_api_key":            "",
		"default_container_image":  "glukw/openclaw-vnc-chrome:latest",
		"default_vnc_resolution":   "1920x1080",
		"orchestrator_backend":     "auto",
		"default_models":           "[]",
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

// migrateAPIKeys migrates old per-instance AnthropicAPIKey/OpenAIAPIKey columns
// to the new InstanceAPIKey table, and renames old global LLM settings to the
// new api_key: prefix. This is idempotent.
func migrateAPIKeys() error {
	// Migrate instance-level LLM keys to InstanceAPIKey rows
	var instances []Instance
	DB.Where("anthropic_api_key != '' OR openai_api_key != ''").Find(&instances)
	for _, inst := range instances {
		for _, pair := range []struct{ name, val string }{
			{"ANTHROPIC_API_KEY", inst.AnthropicAPIKey},
			{"OPENAI_API_KEY", inst.OpenAIAPIKey},
		} {
			if pair.val == "" {
				continue
			}
			var count int64
			DB.Model(&InstanceAPIKey{}).Where("instance_id = ? AND key_name = ?", inst.ID, pair.name).Count(&count)
			if count == 0 {
				if err := DB.Create(&InstanceAPIKey{
					InstanceID: inst.ID,
					KeyName:    pair.name,
					KeyValue:   pair.val,
				}).Error; err != nil {
					log.Printf("migrate api key %s for instance %d: %v", pair.name, inst.ID, err)
				}
			}
		}
		// Clear old columns
		DB.Model(&inst).Updates(map[string]interface{}{
			"anthropic_api_key": "",
			"openai_api_key":    "",
		})
	}

	// Migrate global settings: anthropic_api_key → api_key:ANTHROPIC_API_KEY, etc.
	migrations := map[string]string{
		"anthropic_api_key": "api_key:ANTHROPIC_API_KEY",
		"openai_api_key":    "api_key:OPENAI_API_KEY",
	}
	for oldKey, newKey := range migrations {
		var s Setting
		if err := DB.Where("key = ?", oldKey).First(&s).Error; err == nil && s.Value != "" {
			var count int64
			DB.Model(&Setting{}).Where("key = ?", newKey).Count(&count)
			if count == 0 {
				DB.Create(&Setting{Key: newKey, Value: s.Value})
			}
		}
	}

	return nil
}

// migrateSortOrder sets sort_order = id for existing rows that still have the default 0.
func migrateSortOrder() error {
	return DB.Model(&Instance{}).Where("sort_order = 0").Update("sort_order", gorm.Expr("id")).Error
}

func GetSetting(key string) (string, error) {
	var s Setting
	if err := DB.Where("key = ?", key).First(&s).Error; err != nil {
		return "", err
	}
	return s.Value, nil
}

func SetSetting(key, value string) error {
	return DB.Where("key = ?", key).Assign(Setting{Value: value}).FirstOrCreate(&Setting{Key: key}).Error
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
