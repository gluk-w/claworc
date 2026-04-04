package utils

import (
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&database.Setting{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	setupTestDB(t)
	plaintext := "sk-secret-api-key-12345"
	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext == plaintext {
		t.Error("ciphertext equals plaintext")
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("Decrypt = %q, want %q", decrypted, plaintext)
	}
}

func TestDecrypt_EmptyString(t *testing.T) {
	setupTestDB(t)
	result, err := Decrypt("")
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}
	if result != "" {
		t.Errorf("Decrypt empty = %q, want empty", result)
	}
}

func TestDecrypt_InvalidToken(t *testing.T) {
	setupTestDB(t)
	// Force key generation
	_, _ = Encrypt("init")

	_, err := Decrypt("not-a-valid-fernet-token")
	if err == nil {
		t.Error("expected error for invalid token, got nil")
	}
}

func TestGetKey_AutoGenerates(t *testing.T) {
	setupTestDB(t)

	// No fernet_key exists yet — getKey should auto-generate
	key, err := getKey()
	if err != nil {
		t.Fatalf("getKey: %v", err)
	}
	if key == nil {
		t.Fatal("getKey returned nil key")
	}

	// Verify it was persisted
	stored, err := database.GetSetting("fernet_key")
	if err != nil {
		t.Fatalf("GetSetting fernet_key: %v", err)
	}
	if stored == "" {
		t.Error("fernet_key not stored in settings")
	}
}

func TestGetKey_Idempotent(t *testing.T) {
	setupTestDB(t)

	key1, err := getKey()
	if err != nil {
		t.Fatalf("getKey 1: %v", err)
	}
	key2, err := getKey()
	if err != nil {
		t.Fatalf("getKey 2: %v", err)
	}

	if key1.Encode() != key2.Encode() {
		t.Error("getKey returned different keys on successive calls")
	}
}

func TestMask_LongValue(t *testing.T) {
	t.Parallel()
	result := Mask("sk-1234567890abcdef")
	if result != "****cdef" {
		t.Errorf("Mask = %q, want %q", result, "****cdef")
	}
}

func TestMask_ShortValue(t *testing.T) {
	t.Parallel()
	result := Mask("abcd")
	if result != "****" {
		t.Errorf("Mask = %q, want %q", result, "****")
	}
}

func TestMask_EmptyValue(t *testing.T) {
	t.Parallel()
	result := Mask("")
	if result != "" {
		t.Errorf("Mask empty = %q, want empty", result)
	}
}

func TestMask_FiveChars(t *testing.T) {
	t.Parallel()
	result := Mask("12345")
	if result != "****2345" {
		t.Errorf("Mask = %q, want %q", result, "****2345")
	}
}

// --- SanitizePath tests ---

func TestSanitizePath_TraversalAttack(t *testing.T) {
	t.Parallel()
	result := SanitizePath("../../etc/passwd")
	if result != "etcpasswd" {
		t.Errorf("SanitizePath = %q, want %q", result, "etcpasswd")
	}
}

func TestSanitizePath_EmptyString(t *testing.T) {
	t.Parallel()
	result := SanitizePath("")
	if result != "invalid" {
		t.Errorf("SanitizePath empty = %q, want %q", result, "invalid")
	}
}

func TestSanitizePath_NormalName(t *testing.T) {
	t.Parallel()
	result := SanitizePath("myfile.txt")
	if result != "myfile.txt" {
		t.Errorf("SanitizePath = %q, want %q", result, "myfile.txt")
	}
}

func TestSanitizePath_OnlyDots(t *testing.T) {
	t.Parallel()
	result := SanitizePath("..")
	if result != "invalid" {
		t.Errorf("SanitizePath = %q, want %q", result, "invalid")
	}
}

func TestSanitizePath_BackslashTraversal(t *testing.T) {
	t.Parallel()
	result := SanitizePath("..\\windows\\system32")
	if result != "windowssystem32" {
		t.Errorf("SanitizePath = %q, want %q", result, "windowssystem32")
	}
}
