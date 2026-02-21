package sshaudit

import (
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	if err := db.AutoMigrate(&database.SSHAuditLog{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	return db
}

func TestNewAuditor_DefaultRetention(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 0)
	if a.RetentionDays() != DefaultRetentionDays {
		t.Errorf("expected default retention %d, got %d", DefaultRetentionDays, a.RetentionDays())
	}
}

func TestNewAuditor_CustomRetention(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 30)
	if a.RetentionDays() != 30 {
		t.Errorf("expected retention 30, got %d", a.RetentionDays())
	}
}

func TestLog_WritesToDB(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	err := a.Log(AuditEntry{
		InstanceID:   1,
		InstanceName: "bot-test",
		EventType:    EventConnectionEstablished,
		Username:     "admin",
		SourceIP:     "192.168.1.1",
		Details:      "test connection",
	})
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	var count int64
	db.Model(&database.SSHAuditLog{}).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}

	var record database.SSHAuditLog
	db.First(&record)
	if record.InstanceName != "bot-test" {
		t.Errorf("expected instance_name 'bot-test', got %q", record.InstanceName)
	}
	if record.EventType != EventConnectionEstablished {
		t.Errorf("expected event_type %q, got %q", EventConnectionEstablished, record.EventType)
	}
	if record.Username != "admin" {
		t.Errorf("expected username 'admin', got %q", record.Username)
	}
	if record.SourceIP != "192.168.1.1" {
		t.Errorf("expected source_ip '192.168.1.1', got %q", record.SourceIP)
	}
}

func TestLog_MultipleEvents(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	events := []AuditEntry{
		{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin"},
		{InstanceID: 1, InstanceName: "bot-a", EventType: EventCommandExecution, Username: "admin", Details: "cmd=ls result=ok"},
		{InstanceID: 2, InstanceName: "bot-b", EventType: EventTerminalSessionStart, Username: "viewer"},
		{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionTerminated, Username: "admin", DurationMs: 5000},
	}

	for _, e := range events {
		if err := a.Log(e); err != nil {
			t.Fatalf("Log failed: %v", err)
		}
	}

	var count int64
	db.Model(&database.SSHAuditLog{}).Count(&count)
	if count != 4 {
		t.Errorf("expected 4 records, got %d", count)
	}
}

func TestQuery_NoFilters(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	for i := 0; i < 5; i++ {
		a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-test", EventType: EventConnectionEstablished, Username: "admin"})
	}

	result, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("expected total 5, got %d", result.Total)
	}
	if len(result.Entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(result.Entries))
	}
}

func TestQuery_FilterByInstanceID(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 2, InstanceName: "bot-b", EventType: EventConnectionEstablished, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionTerminated, Username: "admin"})

	result, err := a.Query(QueryOptions{InstanceID: 1})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected total 2, got %d", result.Total)
	}
}

func TestQuery_FilterByEventType(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventFileOperation, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventFileOperation, Username: "viewer"})

	result, err := a.Query(QueryOptions{EventType: EventFileOperation})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected total 2, got %d", result.Total)
	}
}

func TestQuery_FilterByUsername(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventFileOperation, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventFileOperation, Username: "viewer"})
	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "viewer"})

	result, err := a.Query(QueryOptions{Username: "viewer"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected total 2, got %d", result.Total)
	}
}

func TestQuery_FilterByInstanceName(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 2, InstanceName: "bot-b", EventType: EventConnectionEstablished, Username: "admin"})

	result, err := a.Query(QueryOptions{InstanceName: "bot-a"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
}

func TestQuery_FilterByTimeRange(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	// Create entries with specific timestamps
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	twoDaysAgo := now.Add(-48 * time.Hour)

	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: twoDaysAgo})
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: yesterday})
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: now})

	since := now.Add(-25 * time.Hour)
	result, err := a.Query(QueryOptions{Since: &since})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected total 2, got %d", result.Total)
	}

	until := now.Add(-36 * time.Hour)
	result2, err := a.Query(QueryOptions{Until: &until})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result2.Total != 1 {
		t.Errorf("expected total 1, got %d", result2.Total)
	}
}

func TestQuery_Pagination(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	for i := 0; i < 10; i++ {
		a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin"})
	}

	result, err := a.Query(QueryOptions{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 10 {
		t.Errorf("expected total 10, got %d", result.Total)
	}
	if len(result.Entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(result.Entries))
	}
	if result.Limit != 3 {
		t.Errorf("expected limit 3, got %d", result.Limit)
	}

	result2, err := a.Query(QueryOptions{Limit: 3, Offset: 8})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(result2.Entries) != 2 {
		t.Errorf("expected 2 entries at offset 8, got %d", len(result2.Entries))
	}
}

func TestQuery_DefaultLimit(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	result, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Limit != 50 {
		t.Errorf("expected default limit 50, got %d", result.Limit)
	}
}

func TestQuery_MaxLimitCap(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	result, err := a.Query(QueryOptions{Limit: 5000})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Limit != 1000 {
		t.Errorf("expected max limit 1000, got %d", result.Limit)
	}
}

func TestQuery_OrderDescending(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	now := time.Now()
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: "first", Username: "admin", CreatedAt: now.Add(-2 * time.Hour)})
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: "second", Username: "admin", CreatedAt: now.Add(-1 * time.Hour)})
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: "third", Username: "admin", CreatedAt: now})

	result, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result.Entries))
	}
	// Most recent first
	if result.Entries[0].EventType != "third" {
		t.Errorf("expected first entry to be 'third', got %q", result.Entries[0].EventType)
	}
	if result.Entries[2].EventType != "first" {
		t.Errorf("expected last entry to be 'first', got %q", result.Entries[2].EventType)
	}
}

func TestPurgeOlderThan(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	now := time.Now()
	a.SetNowFunc(func() time.Time { return now })

	// Create old and new entries
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -100)})
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -50)})
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -10)})

	deleted, err := a.PurgeOlderThan(90)
	if err != nil {
		t.Fatalf("PurgeOlderThan failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	var count int64
	db.Model(&database.SSHAuditLog{}).Count(&count)
	if count != 2 {
		t.Errorf("expected 2 remaining, got %d", count)
	}
}

func TestPurgeOlderThan_DefaultRetention(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 30)

	now := time.Now()
	a.SetNowFunc(func() time.Time { return now })

	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -35)})
	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -25)})

	// Pass 0 to use configured default (30 days)
	deleted, err := a.PurgeOlderThan(0)
	if err != nil {
		t.Fatalf("PurgeOlderThan failed: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}
}

func TestPurgeOlderThan_NothingToDelete(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	now := time.Now()
	a.SetNowFunc(func() time.Time { return now })

	db.Create(&database.SSHAuditLog{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin", CreatedAt: now.AddDate(0, 0, -10)})

	deleted, err := a.PurgeOlderThan(90)
	if err != nil {
		t.Fatalf("PurgeOlderThan failed: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}

func TestLog_DurationField(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	err := a.Log(AuditEntry{
		InstanceID:   1,
		InstanceName: "bot-test",
		EventType:    EventConnectionTerminated,
		Username:     "admin",
		DurationMs:   12345,
		Details:      "normal disconnect",
	})
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	var record database.SSHAuditLog
	db.First(&record)
	if record.Duration != 12345 {
		t.Errorf("expected duration 12345, got %d", record.Duration)
	}
}

func TestQuery_CombinedFilters(t *testing.T) {
	db := setupTestDB(t)
	a := NewAuditor(db, 90)

	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventFileOperation, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventFileOperation, Username: "viewer"})
	a.Log(AuditEntry{InstanceID: 2, InstanceName: "bot-b", EventType: EventFileOperation, Username: "admin"})
	a.Log(AuditEntry{InstanceID: 1, InstanceName: "bot-a", EventType: EventConnectionEstablished, Username: "admin"})

	result, err := a.Query(QueryOptions{
		InstanceID: 1,
		EventType:  EventFileOperation,
		Username:   "admin",
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
}

func TestRegistry(t *testing.T) {
	// Test global auditor registry
	ResetGlobalForTest()
	if GetAuditor() != nil {
		t.Error("expected nil auditor after reset")
	}

	db := setupTestDB(t)
	a := NewAuditor(db, 90)
	SetGlobalForTest(a)

	if GetAuditor() == nil {
		t.Error("expected non-nil auditor after set")
	}

	if GetAuditor() != a {
		t.Error("expected same auditor instance")
	}

	ResetGlobalForTest()
	if GetAuditor() != nil {
		t.Error("expected nil auditor after reset")
	}
}

func TestEventTypes(t *testing.T) {
	// Verify all event type constants are defined
	eventTypes := []string{
		EventConnectionEstablished,
		EventConnectionTerminated,
		EventCommandExecution,
		EventFileOperation,
		EventTerminalSessionStart,
		EventTerminalSessionEnd,
		EventKeyRotation,
		EventConnectionFailed,
	}

	for _, et := range eventTypes {
		if et == "" {
			t.Error("event type constant is empty")
		}
	}

	// Verify uniqueness
	seen := make(map[string]bool)
	for _, et := range eventTypes {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}
