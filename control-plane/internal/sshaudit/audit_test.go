package sshaudit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Use a temp file DB so multiple SQL connections see the same data (required
	// for concurrent writes). Each test gets its own file via t.TempDir().
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return db
}

func newTestAuditor(t *testing.T) *Auditor {
	t.Helper()
	db := setupTestDB(t)
	a, err := NewAuditor(db, 90)
	if err != nil {
		t.Fatalf("new auditor: %v", err)
	}
	return a
}

// --- NewAuditor tests ---

func TestNewAuditor_CreatesTable(t *testing.T) {
	db := setupTestDB(t)
	_, err := NewAuditor(db, 90)
	if err != nil {
		t.Fatalf("new auditor: %v", err)
	}

	// Verify the table exists by querying it
	var count int64
	if err := db.Model(&AuditEntry{}).Count(&count).Error; err != nil {
		t.Fatalf("query audit table: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 entries in new table, got %d", count)
	}
}

func TestNewAuditor_RetentionDays(t *testing.T) {
	a := newTestAuditor(t)
	if a.RetentionDays() != 90 {
		t.Errorf("expected 90 retention days, got %d", a.RetentionDays())
	}
}

// --- Log tests ---

func TestLog_BasicEvent(t *testing.T) {
	a := newTestAuditor(t)

	a.Log(EventConnection, 1, "admin", "connected to instance 1")

	entries, total, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 total, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.EventType != string(EventConnection) {
		t.Errorf("event_type = %q, want %q", e.EventType, EventConnection)
	}
	if e.InstanceID != 1 {
		t.Errorf("instance_id = %d, want 1", e.InstanceID)
	}
	if e.User != "admin" {
		t.Errorf("user = %q, want %q", e.User, "admin")
	}
	if e.Details != "connected to instance 1" {
		t.Errorf("details = %q, want %q", e.Details, "connected to instance 1")
	}
	if e.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if e.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
}

func TestLog_AllEventTypes(t *testing.T) {
	a := newTestAuditor(t)

	types := []EventType{
		EventConnection,
		EventDisconnection,
		EventCommandExec,
		EventFileOperation,
		EventTerminalSession,
		EventKeyUpload,
		EventKeyRotation,
	}

	for _, et := range types {
		a.Log(et, 1, "user", "test "+string(et))
	}

	_, total, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != int64(len(types)) {
		t.Fatalf("expected %d entries, got %d", len(types), total)
	}
}

// --- Convenience method tests ---

func TestLogConnection(t *testing.T) {
	a := newTestAuditor(t)
	a.LogConnection(1, "admin", "connected")

	entries, _, _ := a.Query(QueryOptions{})
	if entries[0].EventType != string(EventConnection) {
		t.Errorf("expected EventConnection, got %s", entries[0].EventType)
	}
}

func TestLogDisconnection(t *testing.T) {
	a := newTestAuditor(t)
	a.LogDisconnection(1, "admin", "disconnected after 5m")

	entries, _, _ := a.Query(QueryOptions{})
	if entries[0].EventType != string(EventDisconnection) {
		t.Errorf("expected EventDisconnection, got %s", entries[0].EventType)
	}
}

func TestLogCommandExec(t *testing.T) {
	a := newTestAuditor(t)
	a.LogCommandExec(2, "admin", "command=ls, result=success")

	entries, _, _ := a.Query(QueryOptions{})
	if entries[0].EventType != string(EventCommandExec) {
		t.Errorf("expected EventCommandExec, got %s", entries[0].EventType)
	}
	if entries[0].InstanceID != 2 {
		t.Errorf("instance_id = %d, want 2", entries[0].InstanceID)
	}
}

func TestLogFileOperation(t *testing.T) {
	a := newTestAuditor(t)
	a.LogFileOperation(3, "admin", "op=read, path=/etc/hosts")

	entries, _, _ := a.Query(QueryOptions{})
	if entries[0].EventType != string(EventFileOperation) {
		t.Errorf("expected EventFileOperation, got %s", entries[0].EventType)
	}
}

func TestLogTerminalSession(t *testing.T) {
	a := newTestAuditor(t)
	a.LogTerminalSession(1, "admin", "session started")

	entries, _, _ := a.Query(QueryOptions{})
	if entries[0].EventType != string(EventTerminalSession) {
		t.Errorf("expected EventTerminalSession, got %s", entries[0].EventType)
	}
}

func TestLogKeyUpload(t *testing.T) {
	a := newTestAuditor(t)
	a.LogKeyUpload(1, "uploaded public key SHA256:abc")

	entries, _, _ := a.Query(QueryOptions{})
	if entries[0].EventType != string(EventKeyUpload) {
		t.Errorf("expected EventKeyUpload, got %s", entries[0].EventType)
	}
	if entries[0].User != "system" {
		t.Errorf("user = %q, want %q", entries[0].User, "system")
	}
}

func TestLogKeyRotation(t *testing.T) {
	a := newTestAuditor(t)
	a.LogKeyRotation("rotated from SHA256:old to SHA256:new, 3 instances updated")

	entries, _, _ := a.Query(QueryOptions{})
	if entries[0].EventType != string(EventKeyRotation) {
		t.Errorf("expected EventKeyRotation, got %s", entries[0].EventType)
	}
	if entries[0].InstanceID != 0 {
		t.Errorf("instance_id = %d, want 0 for global event", entries[0].InstanceID)
	}
}

// --- Query tests ---

func TestQuery_FilterByInstanceID(t *testing.T) {
	a := newTestAuditor(t)

	a.Log(EventConnection, 1, "admin", "connect 1")
	a.Log(EventConnection, 2, "admin", "connect 2")
	a.Log(EventDisconnection, 1, "admin", "disconnect 1")

	instanceID := uint(1)
	entries, total, err := a.Query(QueryOptions{InstanceID: &instanceID})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 entries for instance 1, got %d", total)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.InstanceID != 1 {
			t.Errorf("expected instance_id 1, got %d", e.InstanceID)
		}
	}
}

func TestQuery_FilterByEventType(t *testing.T) {
	a := newTestAuditor(t)

	a.Log(EventConnection, 1, "admin", "connect")
	a.Log(EventDisconnection, 1, "admin", "disconnect")
	a.Log(EventConnection, 2, "admin", "connect 2")

	et := EventConnection
	entries, total, err := a.Query(QueryOptions{EventType: &et})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 connection events, got %d", total)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestQuery_FilterByBoth(t *testing.T) {
	a := newTestAuditor(t)

	a.Log(EventConnection, 1, "admin", "connect 1")
	a.Log(EventDisconnection, 1, "admin", "disconnect 1")
	a.Log(EventConnection, 2, "admin", "connect 2")

	instanceID := uint(1)
	et := EventConnection
	entries, total, err := a.Query(QueryOptions{InstanceID: &instanceID, EventType: &et})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 matching entry, got %d", total)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Details != "connect 1" {
		t.Errorf("details = %q, want %q", entries[0].Details, "connect 1")
	}
}

func TestQuery_Pagination(t *testing.T) {
	a := newTestAuditor(t)

	for i := 0; i < 25; i++ {
		a.Log(EventConnection, 1, "admin", "connect")
	}

	// First page
	entries, total, err := a.Query(QueryOptions{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 25 {
		t.Fatalf("expected total 25, got %d", total)
	}
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(entries))
	}

	// Second page
	entries2, _, err := a.Query(QueryOptions{Limit: 10, Offset: 10})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries2) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(entries2))
	}

	// Third page (partial)
	entries3, _, err := a.Query(QueryOptions{Limit: 10, Offset: 20})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries3) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries3))
	}

	// Verify no overlap between pages
	ids := make(map[uint]bool)
	for _, e := range entries {
		ids[e.ID] = true
	}
	for _, e := range entries2 {
		if ids[e.ID] {
			t.Errorf("duplicate entry ID %d across pages", e.ID)
		}
		ids[e.ID] = true
	}
}

func TestQuery_DefaultLimit(t *testing.T) {
	a := newTestAuditor(t)

	for i := 0; i < 150; i++ {
		a.Log(EventConnection, 1, "admin", "connect")
	}

	entries, total, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 150 {
		t.Fatalf("expected total 150, got %d", total)
	}
	if len(entries) != 100 {
		t.Fatalf("expected default limit of 100, got %d", len(entries))
	}
}

func TestQuery_OrderNewestFirst(t *testing.T) {
	a := newTestAuditor(t)

	a.Log(EventConnection, 1, "admin", "first")
	time.Sleep(10 * time.Millisecond)
	a.Log(EventConnection, 1, "admin", "second")
	time.Sleep(10 * time.Millisecond)
	a.Log(EventConnection, 1, "admin", "third")

	entries, _, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Newest first
	if entries[0].Details != "third" {
		t.Errorf("first entry should be 'third', got %q", entries[0].Details)
	}
	if entries[2].Details != "first" {
		t.Errorf("last entry should be 'first', got %q", entries[2].Details)
	}
}

func TestQuery_EmptyResult(t *testing.T) {
	a := newTestAuditor(t)

	entries, total, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 0 {
		t.Errorf("expected 0 total, got %d", total)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// --- Purge tests ---

func TestPurgeOlderThan(t *testing.T) {
	db := setupTestDB(t)
	a, _ := NewAuditor(db, 90)

	// Insert entries with explicit timestamps
	old := AuditEntry{
		EventType:  string(EventConnection),
		InstanceID: 1,
		User:       "admin",
		Details:    "old entry",
		CreatedAt:  time.Now().Add(-100 * 24 * time.Hour),
	}
	recent := AuditEntry{
		EventType:  string(EventConnection),
		InstanceID: 1,
		User:       "admin",
		Details:    "recent entry",
		CreatedAt:  time.Now().Add(-1 * time.Hour),
	}
	db.Create(&old)
	db.Create(&recent)

	deleted, err := a.PurgeOlderThan(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	entries, total, _ := a.Query(QueryOptions{})
	if total != 1 {
		t.Fatalf("expected 1 remaining, got %d", total)
	}
	if entries[0].Details != "recent entry" {
		t.Errorf("expected recent entry to remain, got %q", entries[0].Details)
	}
}

func TestPurgeOlderThan_NothingToDelete(t *testing.T) {
	a := newTestAuditor(t)

	a.Log(EventConnection, 1, "admin", "recent")

	deleted, err := a.PurgeOlderThan(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}

func TestPurgeOlderThan_AllDeleted(t *testing.T) {
	db := setupTestDB(t)
	a, _ := NewAuditor(db, 90)

	for i := 0; i < 5; i++ {
		e := AuditEntry{
			EventType:  string(EventConnection),
			InstanceID: 1,
			User:       "admin",
			Details:    "old",
			CreatedAt:  time.Now().Add(-200 * 24 * time.Hour),
		}
		db.Create(&e)
	}

	deleted, err := a.PurgeOlderThan(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 5 {
		t.Errorf("expected 5 deleted, got %d", deleted)
	}

	_, total, _ := a.Query(QueryOptions{})
	if total != 0 {
		t.Errorf("expected 0 remaining, got %d", total)
	}
}

// --- Retention policy tests ---

func TestSetRetentionDays(t *testing.T) {
	a := newTestAuditor(t)

	a.SetRetentionDays(30)
	if a.RetentionDays() != 30 {
		t.Errorf("expected 30, got %d", a.RetentionDays())
	}

	a.SetRetentionDays(0)
	if a.RetentionDays() != 0 {
		t.Errorf("expected 0, got %d", a.RetentionDays())
	}
}

func TestStartRetentionCleanup_CancelStops(t *testing.T) {
	a := newTestAuditor(t)
	cancel := a.StartRetentionCleanup(context.Background())
	// Should not panic
	cancel()
}

// --- Concurrent access tests ---

func TestConcurrentLogging(t *testing.T) {
	a := newTestAuditor(t)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 10; j++ {
				a.Log(EventConnection, uint(id), "user", "concurrent")
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	_, total, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 100 {
		t.Errorf("expected 100 entries from concurrent logging, got %d", total)
	}
}

// --- Table name test ---

func TestAuditEntry_TableName(t *testing.T) {
	e := AuditEntry{}
	if e.TableName() != "ssh_audit_logs" {
		t.Errorf("table name = %q, want %q", e.TableName(), "ssh_audit_logs")
	}
}
