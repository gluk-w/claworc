package sshaudit

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- Audit Logging Security Tests ---

// TestSecurity_AllSSHEventTypesLogged verifies that all defined SSH event types
// can be logged and retrieved correctly, ensuring comprehensive audit coverage.
func TestSecurity_AllSSHEventTypesLogged(t *testing.T) {
	a := newTestAuditor(t)

	eventTypes := []struct {
		eventType EventType
		logFunc   func()
	}{
		{EventConnection, func() { a.LogConnection(1, "admin", "SSH connected to instance 1") }},
		{EventDisconnection, func() { a.LogDisconnection(1, "admin", "duration: 5m, reason: user disconnect") }},
		{EventCommandExec, func() { a.LogCommandExec(1, "admin", "command: ls -la, exit_code: 0") }},
		{EventFileOperation, func() { a.LogFileOperation(1, "admin", "op: read, path: /etc/hosts") }},
		{EventTerminalSession, func() { a.LogTerminalSession(1, "admin", "session started, id: abc-123") }},
		{EventKeyUpload, func() { a.LogKeyUpload(1, "uploaded key SHA256:abc123") }},
		{EventKeyRotation, func() { a.LogKeyRotation("old=SHA256:old, new=SHA256:new, 3 instances updated") }},
	}

	for _, et := range eventTypes {
		et.logFunc()
	}

	// Verify each event type was logged
	for _, et := range eventTypes {
		evType := et.eventType
		entries, total, err := a.Query(QueryOptions{EventType: &evType})
		if err != nil {
			t.Fatalf("query for %s: %v", et.eventType, err)
		}
		if total != 1 {
			t.Errorf("SECURITY: event type %s not logged (total=%d)", et.eventType, total)
		}
		if len(entries) != 1 {
			t.Errorf("SECURITY: expected 1 entry for %s, got %d", et.eventType, len(entries))
			continue
		}

		// Verify basic fields
		entry := entries[0]
		if entry.EventType != string(et.eventType) {
			t.Errorf("SECURITY: event type mismatch: got %s, want %s", entry.EventType, et.eventType)
		}
		if entry.Details == "" {
			t.Errorf("SECURITY: event %s has empty details", et.eventType)
		}
		if entry.CreatedAt.IsZero() {
			t.Errorf("SECURITY: event %s has zero timestamp", et.eventType)
		}
	}

	// Verify total count
	_, total, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if total != int64(len(eventTypes)) {
		t.Errorf("SECURITY: expected %d total events, got %d", len(eventTypes), total)
	}
}

// TestSecurity_AuditEntriesHaveTimestamps verifies that all audit entries
// have accurate timestamps for forensic analysis.
func TestSecurity_AuditEntriesHaveTimestamps(t *testing.T) {
	a := newTestAuditor(t)

	before := time.Now().Add(-time.Second)
	a.LogConnection(1, "admin", "connected")
	after := time.Now().Add(time.Second)

	entries, _, _ := a.Query(QueryOptions{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	ts := entries[0].CreatedAt
	if ts.Before(before) || ts.After(after) {
		t.Errorf("SECURITY: timestamp %v not within expected range [%v, %v]", ts, before, after)
	}
}

// TestSecurity_AuditEntriesPreserveDetails verifies that audit log details
// are preserved exactly as logged, supporting forensic investigation.
func TestSecurity_AuditEntriesPreserveDetails(t *testing.T) {
	a := newTestAuditor(t)

	details := "command: rm -rf /tmp/malicious; exit_code: 0; user_ip: 10.0.0.5"
	a.LogCommandExec(42, "attacker", details)

	entries, _, _ := a.Query(QueryOptions{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Details != details {
		t.Errorf("SECURITY: details not preserved: got %q, want %q", entries[0].Details, details)
	}
	if entries[0].User != "attacker" {
		t.Errorf("SECURITY: user not preserved: got %q, want %q", entries[0].User, "attacker")
	}
	if entries[0].InstanceID != 42 {
		t.Errorf("SECURITY: instance_id not preserved: got %d, want 42", entries[0].InstanceID)
	}
}

// TestSecurity_AuditQueryFiltersByInstanceID verifies that audit logs can be
// filtered by instance ID for targeted security investigations.
func TestSecurity_AuditQueryFiltersByInstanceID(t *testing.T) {
	a := newTestAuditor(t)

	// Log events for multiple instances
	a.LogConnection(1, "admin", "connect to instance 1")
	a.LogConnection(2, "admin", "connect to instance 2")
	a.LogCommandExec(1, "admin", "command on instance 1")
	a.LogCommandExec(3, "admin", "command on instance 3")

	// Query for instance 1 only
	instanceID := uint(1)
	entries, total, err := a.Query(QueryOptions{InstanceID: &instanceID})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 2 {
		t.Errorf("SECURITY: expected 2 events for instance 1, got %d", total)
	}
	for _, e := range entries {
		if e.InstanceID != 1 {
			t.Errorf("SECURITY: query leaked events from instance %d", e.InstanceID)
		}
	}
}

// TestSecurity_AuditQueryFiltersByEventType verifies that audit logs can be
// filtered by event type for security analysis.
func TestSecurity_AuditQueryFiltersByEventType(t *testing.T) {
	a := newTestAuditor(t)

	a.LogConnection(1, "admin", "connect")
	a.LogCommandExec(1, "admin", "dangerous command")
	a.LogCommandExec(2, "admin", "another command")
	a.LogFileOperation(1, "admin", "file read")

	// Query for command_exec events only
	et := EventCommandExec
	entries, total, err := a.Query(QueryOptions{EventType: &et})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 2 {
		t.Errorf("SECURITY: expected 2 command_exec events, got %d", total)
	}
	for _, e := range entries {
		if e.EventType != string(EventCommandExec) {
			t.Errorf("SECURITY: query returned wrong event type: %s", e.EventType)
		}
	}
}

// TestSecurity_RetentionPolicyDeletesOldEntries verifies that the retention
// policy correctly purges old audit entries while keeping recent ones.
func TestSecurity_RetentionPolicyDeletesOldEntries(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	a, err := NewAuditor(db, 90)
	if err != nil {
		t.Fatalf("new auditor: %v", err)
	}

	// Insert entries with different ages
	oldEntry := AuditEntry{
		EventType:  string(EventConnection),
		InstanceID: 1,
		User:       "admin",
		Details:    "old connection (should be purged)",
		CreatedAt:  time.Now().Add(-100 * 24 * time.Hour), // 100 days old
	}
	recentEntry := AuditEntry{
		EventType:  string(EventConnection),
		InstanceID: 1,
		User:       "admin",
		Details:    "recent connection (should be kept)",
		CreatedAt:  time.Now().Add(-1 * time.Hour), // 1 hour old
	}
	db.Create(&oldEntry)
	db.Create(&recentEntry)

	// Purge entries older than 90 days
	deleted, err := a.PurgeOlderThan(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if deleted != 1 {
		t.Errorf("SECURITY: expected 1 entry purged, got %d", deleted)
	}

	// Verify only recent entry remains
	entries, total, _ := a.Query(QueryOptions{})
	if total != 1 {
		t.Errorf("SECURITY: expected 1 remaining entry, got %d", total)
	}
	if len(entries) > 0 && entries[0].Details != "recent connection (should be kept)" {
		t.Error("SECURITY: wrong entry survived purge")
	}
}

// TestSecurity_RetentionPolicyConfigurable verifies that the retention policy
// can be updated at runtime without restart.
func TestSecurity_RetentionPolicyConfigurable(t *testing.T) {
	a := newTestAuditor(t)

	if a.RetentionDays() != 90 {
		t.Errorf("expected initial retention of 90 days, got %d", a.RetentionDays())
	}

	a.SetRetentionDays(30)
	if a.RetentionDays() != 30 {
		t.Errorf("expected retention of 30 days after update, got %d", a.RetentionDays())
	}

	a.SetRetentionDays(365)
	if a.RetentionDays() != 365 {
		t.Errorf("expected retention of 365 days after update, got %d", a.RetentionDays())
	}
}

// TestSecurity_AuditLogOrderNewestFirst verifies that audit queries return
// entries newest-first for efficient security investigation.
func TestSecurity_AuditLogOrderNewestFirst(t *testing.T) {
	a := newTestAuditor(t)

	// Log events with deliberate ordering
	a.Log(EventConnection, 1, "admin", "first event")
	time.Sleep(10 * time.Millisecond)
	a.Log(EventCommandExec, 1, "admin", "second event")
	time.Sleep(10 * time.Millisecond)
	a.Log(EventDisconnection, 1, "admin", "third event")

	entries, _, _ := a.Query(QueryOptions{})
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Newest first
	if entries[0].Details != "third event" {
		t.Errorf("SECURITY: first result should be newest, got %q", entries[0].Details)
	}
	if entries[2].Details != "first event" {
		t.Errorf("SECURITY: last result should be oldest, got %q", entries[2].Details)
	}
}

// TestSecurity_ConcurrentAuditLogWritesAreThreadSafe verifies that concurrent
// audit log writes don't lose entries or corrupt data.
func TestSecurity_ConcurrentAuditLogWritesAreThreadSafe(t *testing.T) {
	a := newTestAuditor(t)

	var wg sync.WaitGroup
	entriesPerGoroutine := 20
	goroutineCount := 10

	for g := 0; g < goroutineCount; g++ {
		wg.Add(1)
		go func(instanceID int) {
			defer wg.Done()
			for i := 0; i < entriesPerGoroutine; i++ {
				a.LogConnection(uint(instanceID), "admin", "concurrent test")
			}
		}(g + 1)
	}

	wg.Wait()

	_, total, err := a.Query(QueryOptions{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	expected := int64(goroutineCount * entriesPerGoroutine)
	if total != expected {
		t.Errorf("SECURITY: expected %d audit entries from concurrent writes, got %d (data loss detected)", expected, total)
	}
}

// TestSecurity_KeyUploadLogsSystemUser verifies that key upload events
// are logged with "system" as the user, distinguishing automated operations.
func TestSecurity_KeyUploadLogsSystemUser(t *testing.T) {
	a := newTestAuditor(t)

	a.LogKeyUpload(1, "uploaded public key SHA256:abc123")

	entries, _, _ := a.Query(QueryOptions{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].User != "system" {
		t.Errorf("SECURITY: key upload should be logged as 'system' user, got %q", entries[0].User)
	}
}

// TestSecurity_KeyRotationLogsGlobalEvent verifies that key rotation events
// are logged with instance_id=0 (global) and user="system".
func TestSecurity_KeyRotationLogsGlobalEvent(t *testing.T) {
	a := newTestAuditor(t)

	a.LogKeyRotation("old=SHA256:old, new=SHA256:new, 5 instances updated")

	entries, _, _ := a.Query(QueryOptions{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].InstanceID != 0 {
		t.Errorf("SECURITY: key rotation should use instance_id=0, got %d", entries[0].InstanceID)
	}
	if entries[0].User != "system" {
		t.Errorf("SECURITY: key rotation should be logged as 'system' user, got %q", entries[0].User)
	}
	if entries[0].EventType != string(EventKeyRotation) {
		t.Errorf("SECURITY: expected event_type %s, got %s", EventKeyRotation, entries[0].EventType)
	}
}

// TestSecurity_AuditPaginationPreventsDataLeakBetweenPages verifies that
// paginated queries don't duplicate or skip entries.
func TestSecurity_AuditPaginationPreventsDataLeakBetweenPages(t *testing.T) {
	a := newTestAuditor(t)

	totalEntries := 30
	for i := 0; i < totalEntries; i++ {
		a.Log(EventConnection, uint(i%5+1), "admin", "event")
	}

	// Paginate through all entries
	pageSize := 10
	allIDs := make(map[uint]bool)
	for offset := 0; offset < totalEntries; offset += pageSize {
		entries, total, err := a.Query(QueryOptions{Limit: pageSize, Offset: offset})
		if err != nil {
			t.Fatalf("query at offset %d: %v", offset, err)
		}
		if total != int64(totalEntries) {
			t.Errorf("total changed during pagination: got %d, want %d", total, totalEntries)
		}
		for _, e := range entries {
			if allIDs[e.ID] {
				t.Errorf("SECURITY: duplicate entry ID %d across pages (audit data integrity issue)", e.ID)
			}
			allIDs[e.ID] = true
		}
	}

	if len(allIDs) != totalEntries {
		t.Errorf("SECURITY: pagination returned %d unique entries, expected %d (entries lost)", len(allIDs), totalEntries)
	}
}
