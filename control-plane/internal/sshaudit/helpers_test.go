package sshaudit

import (
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupHelperTestDB(t *testing.T) {
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
	a := NewAuditor(db, 90)
	SetGlobalForTest(a)
	t.Cleanup(func() { ResetGlobalForTest() })
}

func countAuditLogs(t *testing.T, eventType string) int64 {
	t.Helper()
	a := GetAuditor()
	result, err := a.Query(QueryOptions{EventType: eventType})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	return result.Total
}

func TestLogConnection(t *testing.T) {
	setupHelperTestDB(t)
	LogConnection(1, "bot-test", "admin", "10.0.0.1")
	if n := countAuditLogs(t, EventConnectionEstablished); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestLogDisconnection(t *testing.T) {
	setupHelperTestDB(t)
	LogDisconnection(1, "bot-test", "admin", "normal", 5000)
	if n := countAuditLogs(t, EventConnectionTerminated); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestLogConnectionFailed(t *testing.T) {
	setupHelperTestDB(t)
	LogConnectionFailed(1, "bot-test", "admin", "timeout")
	if n := countAuditLogs(t, EventConnectionFailed); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestLogCommand(t *testing.T) {
	setupHelperTestDB(t)
	LogCommand(1, "bot-test", "admin", "ls -la", "ok")
	if n := countAuditLogs(t, EventCommandExecution); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestLogFileOperation(t *testing.T) {
	setupHelperTestDB(t)
	LogFileOperation(1, "bot-test", "admin", "read", "/root/test.txt")
	if n := countAuditLogs(t, EventFileOperation); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestLogTerminalSessionStart(t *testing.T) {
	setupHelperTestDB(t)
	LogTerminalSessionStart(1, "bot-test", "admin", "sess-123", "10.0.0.1")
	if n := countAuditLogs(t, EventTerminalSessionStart); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestLogTerminalSessionEnd(t *testing.T) {
	setupHelperTestDB(t)
	LogTerminalSessionEnd(1, "bot-test", "admin", "sess-123", 30000)
	if n := countAuditLogs(t, EventTerminalSessionEnd); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestLogKeyRotation(t *testing.T) {
	setupHelperTestDB(t)
	LogKeyRotation(1, "bot-test", "admin", "SHA256:abc123")
	if n := countAuditLogs(t, EventKeyRotation); n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

func TestHelpers_NilAuditor(t *testing.T) {
	ResetGlobalForTest()
	// These should not panic when auditor is nil
	LogConnection(1, "bot-test", "admin", "10.0.0.1")
	LogDisconnection(1, "bot-test", "admin", "reason", 0)
	LogConnectionFailed(1, "bot-test", "admin", "err")
	LogCommand(1, "bot-test", "admin", "cmd", "ok")
	LogFileOperation(1, "bot-test", "admin", "read", "/path")
	LogTerminalSessionStart(1, "bot-test", "admin", "sess", "ip")
	LogTerminalSessionEnd(1, "bot-test", "admin", "sess", 0)
	LogKeyRotation(1, "bot-test", "admin", "fp")
}

func TestExtractSourceIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.50, 70.41.3.18")
	ip := ExtractSourceIP(r)
	if ip != "203.0.113.50" {
		t.Errorf("expected '203.0.113.50', got %q", ip)
	}
}

func TestExtractSourceIP_XRealIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-Ip", "198.51.100.10")
	ip := ExtractSourceIP(r)
	if ip != "198.51.100.10" {
		t.Errorf("expected '198.51.100.10', got %q", ip)
	}
}

func TestExtractSourceIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:12345"
	ip := ExtractSourceIP(r)
	if ip != "192.0.2.1" {
		t.Errorf("expected '192.0.2.1', got %q", ip)
	}
}

func TestExtractSourceIP_Priority(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.50")
	r.Header.Set("X-Real-Ip", "198.51.100.10")
	r.RemoteAddr = "192.0.2.1:12345"
	// X-Forwarded-For should take priority
	ip := ExtractSourceIP(r)
	if ip != "203.0.113.50" {
		t.Errorf("expected '203.0.113.50', got %q", ip)
	}
}
