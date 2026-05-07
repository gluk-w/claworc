package database

import (
	"testing"
	"time"

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
	if err := db.AutoMigrate(
		&Instance{}, &Setting{}, &User{}, &UserInstance{},
		&WebAuthnCredential{}, &LLMProvider{}, &LLMGatewayKey{},
		&Skill{}, &Backup{}, &BackupSchedule{}, &SharedFolder{},
		&Team{}, &TeamMember{}, &TeamProvider{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	DB = db
	t.Cleanup(func() { DB = nil })
}

// --- Settings ---

func TestGetSetting_NotFound(t *testing.T) {
	setupTestDB(t)
	_, err := GetSetting("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent setting")
	}
}

func TestSetSetting_CreateAndRead(t *testing.T) {
	setupTestDB(t)
	if err := SetSetting("foo", "bar"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	val, err := GetSetting("foo")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if val != "bar" {
		t.Errorf("GetSetting = %q, want %q", val, "bar")
	}
}

func TestSetSetting_Upsert(t *testing.T) {
	setupTestDB(t)
	SetSetting("key", "v1")
	SetSetting("key", "v2")
	val, _ := GetSetting("key")
	if val != "v2" {
		t.Errorf("after upsert got %q, want %q", val, "v2")
	}
}

func TestDeleteSetting(t *testing.T) {
	setupTestDB(t)
	SetSetting("key", "val")
	if err := DeleteSetting("key"); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	_, err := GetSetting("key")
	if err == nil {
		t.Error("setting still exists after delete")
	}
}

// --- Users ---

func TestCreateUser_Success(t *testing.T) {
	setupTestDB(t)
	user := &User{Username: "alice", PasswordHash: "hash123", Role: "admin"}
	if err := CreateUser(user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID == 0 {
		t.Error("user ID not assigned")
	}

	got, err := GetUserByID(user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q, want %q", got.Username, "alice")
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	setupTestDB(t)
	CreateUser(&User{Username: "bob", PasswordHash: "h1", Role: "user"})
	err := CreateUser(&User{Username: "bob", PasswordHash: "h2", Role: "user"})
	if err == nil {
		t.Error("expected error for duplicate username")
	}
}

func TestGetUserByUsername(t *testing.T) {
	setupTestDB(t)
	CreateUser(&User{Username: "charlie", PasswordHash: "h", Role: "user"})

	user, err := GetUserByUsername("charlie")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if user.Username != "charlie" {
		t.Errorf("Username = %q, want %q", user.Username, "charlie")
	}

	_, err = GetUserByUsername("nobody")
	if err == nil {
		t.Error("expected error for nonexistent user")
	}
}

func TestDeleteUser_CascadesAssignments(t *testing.T) {
	setupTestDB(t)
	user := &User{Username: "doomed", PasswordHash: "h", Role: "user"}
	CreateUser(user)

	// Create instance assignment
	DB.Create(&UserInstance{UserID: user.ID, InstanceID: 99})

	// Create WebAuthn credential
	DB.Create(&WebAuthnCredential{ID: "cred1", UserID: user.ID, PublicKey: []byte("key")})

	if err := DeleteUser(user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Verify user gone
	_, err := GetUserByID(user.ID)
	if err == nil {
		t.Error("user still exists after delete")
	}

	// Verify assignments gone
	var assignCount int64
	DB.Model(&UserInstance{}).Where("user_id = ?", user.ID).Count(&assignCount)
	if assignCount > 0 {
		t.Error("user instance assignments still exist after delete")
	}

	// Verify credentials gone
	var credCount int64
	DB.Model(&WebAuthnCredential{}).Where("user_id = ?", user.ID).Count(&credCount)
	if credCount > 0 {
		t.Error("webauthn credentials still exist after delete")
	}
}

func TestUpdateUserPassword(t *testing.T) {
	setupTestDB(t)
	user := &User{Username: "pwuser", PasswordHash: "old", Role: "user"}
	CreateUser(user)

	if err := UpdateUserPassword(user.ID, "newhash"); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}

	got, _ := GetUserByID(user.ID)
	if got.PasswordHash != "newhash" {
		t.Errorf("PasswordHash = %q, want %q", got.PasswordHash, "newhash")
	}
}

func TestListUsers(t *testing.T) {
	setupTestDB(t)
	CreateUser(&User{Username: "a", PasswordHash: "h", Role: "admin"})
	CreateUser(&User{Username: "b", PasswordHash: "h", Role: "user"})

	users, err := ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("len = %d, want 2", len(users))
	}
	// Verify ordering
	if users[0].Username != "a" || users[1].Username != "b" {
		t.Errorf("unexpected order: %s, %s", users[0].Username, users[1].Username)
	}
}

func TestUserCount(t *testing.T) {
	setupTestDB(t)
	count, _ := UserCount()
	if count != 0 {
		t.Errorf("initial count = %d, want 0", count)
	}

	CreateUser(&User{Username: "x", PasswordHash: "h", Role: "user"})
	count, _ = UserCount()
	if count != 1 {
		t.Errorf("count after create = %d, want 1", count)
	}
}

func TestGetFirstAdmin(t *testing.T) {
	setupTestDB(t)

	// No admins
	_, err := GetFirstAdmin()
	if err == nil {
		t.Error("expected error when no admins exist")
	}

	CreateUser(&User{Username: "user1", PasswordHash: "h", Role: "user"})
	CreateUser(&User{Username: "admin1", PasswordHash: "h", Role: "admin"})
	CreateUser(&User{Username: "admin2", PasswordHash: "h", Role: "admin"})

	admin, err := GetFirstAdmin()
	if err != nil {
		t.Fatalf("GetFirstAdmin: %v", err)
	}
	if admin.Username != "admin1" {
		t.Errorf("first admin = %q, want %q", admin.Username, "admin1")
	}
}

// --- Instance Assignments ---

func TestSetUserInstances_ReplacesAll(t *testing.T) {
	setupTestDB(t)
	user := &User{Username: "u", PasswordHash: "h", Role: "user"}
	CreateUser(user)

	SetUserInstances(user.ID, []uint{1, 2})
	ids, _ := GetUserInstances(user.ID)
	if len(ids) != 2 {
		t.Fatalf("after first set: len = %d, want 2", len(ids))
	}

	SetUserInstances(user.ID, []uint{3})
	ids, _ = GetUserInstances(user.ID)
	if len(ids) != 1 || ids[0] != 3 {
		t.Errorf("after second set: ids = %v, want [3]", ids)
	}
}

func TestIsUserAssignedToInstance(t *testing.T) {
	setupTestDB(t)
	user := &User{Username: "u", PasswordHash: "h", Role: "user"}
	CreateUser(user)
	SetUserInstances(user.ID, []uint{10, 20})

	if !IsUserAssignedToInstance(user.ID, 10) {
		t.Error("expected true for assigned instance 10")
	}
	if !IsUserAssignedToInstance(user.ID, 20) {
		t.Error("expected true for assigned instance 20")
	}
	if IsUserAssignedToInstance(user.ID, 30) {
		t.Error("expected false for unassigned instance 30")
	}
}

// --- Backups ---

func TestCreateBackup_ListBackups(t *testing.T) {
	setupTestDB(t)
	b := &Backup{InstanceID: 1, InstanceName: "bot-test", Status: "completed", FilePath: "/tmp/b1.tar"}
	if err := CreateBackup(b); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	backups, err := ListBackups(1)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 1 {
		t.Errorf("len = %d, want 1", len(backups))
	}

	// Different instance
	backups, _ = ListBackups(999)
	if len(backups) != 0 {
		t.Errorf("expected 0 backups for instance 999, got %d", len(backups))
	}
}

func TestGetLatestCompletedBackup(t *testing.T) {
	setupTestDB(t)

	// No backups
	_, err := GetLatestCompletedBackup(1)
	if err == nil {
		t.Error("expected error when no backups exist")
	}

	// Running backup (not completed)
	CreateBackup(&Backup{InstanceID: 1, InstanceName: "t", Status: "running", FilePath: "/a"})
	_, err = GetLatestCompletedBackup(1)
	if err == nil {
		t.Error("expected error when only running backup exists")
	}

	// Add completed backup
	CreateBackup(&Backup{InstanceID: 1, InstanceName: "t", Status: "completed", FilePath: "/b"})
	b, err := GetLatestCompletedBackup(1)
	if err != nil {
		t.Fatalf("GetLatestCompletedBackup: %v", err)
	}
	if b.FilePath != "/b" {
		t.Errorf("FilePath = %q, want %q", b.FilePath, "/b")
	}
}

// --- Shared Folders ---

func TestGetSharedFoldersForInstance(t *testing.T) {
	setupTestDB(t)
	DB.Create(&SharedFolder{Name: "sf1", MountPath: "/data", OwnerID: 1, InstanceIDs: "[1,2]"})
	DB.Create(&SharedFolder{Name: "sf2", MountPath: "/data2", OwnerID: 1, InstanceIDs: "[3]"})

	folders, err := GetSharedFoldersForInstance(1)
	if err != nil {
		t.Fatalf("GetSharedFoldersForInstance: %v", err)
	}
	if len(folders) != 1 || folders[0].Name != "sf1" {
		t.Errorf("got %d folders, want 1 (sf1)", len(folders))
	}

	folders, _ = GetSharedFoldersForInstance(3)
	if len(folders) != 1 || folders[0].Name != "sf2" {
		t.Errorf("got %d folders for instance 3, want 1 (sf2)", len(folders))
	}

	folders, _ = GetSharedFoldersForInstance(99)
	if len(folders) != 0 {
		t.Errorf("got %d folders for instance 99, want 0", len(folders))
	}
}

func TestListSharedFolders_AdminVsUser(t *testing.T) {
	setupTestDB(t)
	DB.Create(&SharedFolder{Name: "mine", MountPath: "/a", OwnerID: 1, InstanceIDs: "[]"})
	DB.Create(&SharedFolder{Name: "theirs", MountPath: "/b", OwnerID: 2, InstanceIDs: "[]"})

	// Admin sees all
	all, _ := ListSharedFolders(1, true)
	if len(all) != 2 {
		t.Errorf("admin sees %d, want 2", len(all))
	}

	// User sees own
	mine, _ := ListSharedFolders(1, false)
	if len(mine) != 1 {
		t.Errorf("user sees %d, want 1", len(mine))
	}
}

// --- BackupSchedule ---

func TestListDueSchedules(t *testing.T) {
	setupTestDB(t)
	past := time.Now().UTC().Add(-1 * time.Hour)
	future := time.Now().UTC().Add(1 * time.Hour)

	DB.Create(&BackupSchedule{InstanceIDs: "[1]", CronExpression: "0 0 * * *", NextRunAt: &past})
	DB.Create(&BackupSchedule{InstanceIDs: "[2]", CronExpression: "0 0 * * *", NextRunAt: &future})

	due, err := ListDueSchedules()
	if err != nil {
		t.Fatalf("ListDueSchedules: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("due schedules = %d, want 1", len(due))
	}
}
