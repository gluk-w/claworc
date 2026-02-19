package database

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gluk-w/claworc/control-plane/internal/config"
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
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000", dbPath)
	DB, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	if err := DB.AutoMigrate(&Instance{}, &Setting{}, &InstanceAPIKey{}, &User{}, &UserInstance{}, &WebAuthnCredential{}); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}

	if err := seedDefaults(); err != nil {
		return fmt.Errorf("seed defaults: %w", err)
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
		"default_container_image":  "glukw/openclaw-vnc-chromium:latest",
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
