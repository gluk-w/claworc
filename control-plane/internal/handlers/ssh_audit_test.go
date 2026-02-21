package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupAuditTestDB initialises an in-memory SQLite DB with audit log table.
func setupAuditTestDB(t *testing.T) func() {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	if err := database.DB.AutoMigrate(&database.Instance{}, &database.User{}, &database.UserInstance{}, &database.SSHAuditLog{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return func() {
		sqlDB, _ := database.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
}

func setupAuditor(t *testing.T) {
	t.Helper()
	auditor := sshaudit.NewAuditor(database.DB, 90)
	sshaudit.SetGlobalForTest(auditor)
	t.Cleanup(func() { sshaudit.ResetGlobalForTest() })
}

func seedAuditLogs(t *testing.T, n int) {
	t.Helper()
	auditor := sshaudit.GetAuditor()
	for i := 0; i < n; i++ {
		auditor.Log(sshaudit.AuditEntry{
			InstanceID:   1,
			InstanceName: "bot-test",
			EventType:    sshaudit.EventConnectionEstablished,
			Username:     "admin",
			SourceIP:     "10.0.0.1",
		})
	}
}

func TestGetSSHAuditLogs_Success(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	seedAuditLogs(t, 5)

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result sshaudit.QueryResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("expected total 5, got %d", result.Total)
	}
	if len(result.Entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(result.Entries))
	}
}

func TestGetSSHAuditLogs_AuditorNotInitialized(t *testing.T) {
	sshaudit.ResetGlobalForTest()

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs", nil)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestGetSSHAuditLogs_FilterByInstanceID(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	auditor := sshaudit.GetAuditor()
	auditor.Log(sshaudit.AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin"})
	auditor.Log(sshaudit.AuditEntry{InstanceID: 2, InstanceName: "bot-b", EventType: sshaudit.EventConnectionEstablished, Username: "admin"})

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?instance_id=1", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result sshaudit.QueryResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
}

func TestGetSSHAuditLogs_FilterByEventType(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	auditor := sshaudit.GetAuditor()
	auditor.Log(sshaudit.AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin"})
	auditor.Log(sshaudit.AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventFileOperation, Username: "admin"})
	auditor.Log(sshaudit.AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventFileOperation, Username: "admin"})

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?event_type=file_operation", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result sshaudit.QueryResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 2 {
		t.Errorf("expected total 2, got %d", result.Total)
	}
}

func TestGetSSHAuditLogs_FilterByUsername(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	auditor := sshaudit.GetAuditor()
	auditor.Log(sshaudit.AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin"})
	auditor.Log(sshaudit.AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "viewer"})

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?username=viewer", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result sshaudit.QueryResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
}

func TestGetSSHAuditLogs_Pagination(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	seedAuditLogs(t, 15)

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?limit=5&offset=10", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result sshaudit.QueryResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 15 {
		t.Errorf("expected total 15, got %d", result.Total)
	}
	if len(result.Entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(result.Entries))
	}
	if result.Limit != 5 {
		t.Errorf("expected limit 5, got %d", result.Limit)
	}
	if result.Offset != 10 {
		t.Errorf("expected offset 10, got %d", result.Offset)
	}
}

func TestGetSSHAuditLogs_InvalidInstanceID(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?instance_id=abc", nil)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHAuditLogs_InvalidSinceTimestamp(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?since=not-a-date", nil)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHAuditLogs_InvalidUntilTimestamp(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?until=not-a-date", nil)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHAuditLogs_InvalidLimit(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?limit=-5", nil)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHAuditLogs_InvalidOffset(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?offset=-1", nil)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHAuditLogs_TimeRangeFilter(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	now := time.Now()
	database.DB.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin", CreatedAt: now.Add(-48 * time.Hour)})
	database.DB.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin", CreatedAt: now.Add(-12 * time.Hour)})
	database.DB.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin", CreatedAt: now})

	since := now.Add(-24 * time.Hour).Format(time.RFC3339)
	r := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/ssh-audit-logs?since=%s", since), nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result sshaudit.QueryResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 2 {
		t.Errorf("expected total 2, got %d", result.Total)
	}
}

func TestPurgeSSHAuditLogs_Success(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	now := time.Now()
	database.DB.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -100)})
	database.DB.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: sshaudit.EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -10)})

	r := httptest.NewRequest("POST", "/api/v1/ssh-audit-logs/purge?days=30", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	PurgeSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["deleted"].(float64) != 1 {
		t.Errorf("expected 1 deleted, got %v", resp["deleted"])
	}
}

func TestPurgeSSHAuditLogs_DefaultRetention(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := httptest.NewRequest("POST", "/api/v1/ssh-audit-logs/purge", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	PurgeSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["retention_days"].(float64) != 90 {
		t.Errorf("expected retention_days 90, got %v", resp["retention_days"])
	}
}

func TestPurgeSSHAuditLogs_AuditorNotInitialized(t *testing.T) {
	sshaudit.ResetGlobalForTest()

	r := httptest.NewRequest("POST", "/api/v1/ssh-audit-logs/purge", nil)
	w := httptest.NewRecorder()

	PurgeSSHAuditLogs(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestPurgeSSHAuditLogs_InvalidDays(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	r := httptest.NewRequest("POST", "/api/v1/ssh-audit-logs/purge?days=-5", nil)
	w := httptest.NewRecorder()

	PurgeSSHAuditLogs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHAuditLogs_FilterByInstanceName(t *testing.T) {
	cleanup := setupAuditTestDB(t)
	defer cleanup()
	setupAuditor(t)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	auditor := sshaudit.GetAuditor()
	auditor.Log(sshaudit.AuditEntry{InstanceID: 1, InstanceName: "bot-alpha", EventType: sshaudit.EventConnectionEstablished, Username: "admin"})
	auditor.Log(sshaudit.AuditEntry{InstanceID: 2, InstanceName: "bot-beta", EventType: sshaudit.EventConnectionEstablished, Username: "admin"})

	r := httptest.NewRequest("GET", "/api/v1/ssh-audit-logs?instance_name=bot-alpha", nil)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()

	GetSSHAuditLogs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result sshaudit.QueryResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
}
