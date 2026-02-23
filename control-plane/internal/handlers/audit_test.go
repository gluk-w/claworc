package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
)

func setupAuditTest(t *testing.T) func() {
	t.Helper()
	setupTestDB(t)

	a, err := sshaudit.NewAuditor(database.DB, 90)
	if err != nil {
		t.Fatalf("new auditor: %v", err)
	}
	AuditLog = a

	return func() {
		AuditLog = nil
	}
}

func TestGetAuditLogs_Empty(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	entries := result["entries"].([]interface{})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
	if total := result["total"].(float64); total != 0 {
		t.Errorf("expected total 0, got %.0f", total)
	}
}

func TestGetAuditLogs_WithEntries(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	AuditLog.LogConnection(1, "admin", "connected")
	AuditLog.LogDisconnection(1, "admin", "disconnected after 5m")
	AuditLog.LogCommandExec(2, "admin", "echo hello")

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	entries := result["entries"].([]interface{})
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if total := result["total"].(float64); total != 3 {
		t.Errorf("expected total 3, got %.0f", total)
	}

	// Verify entry structure
	first := entries[0].(map[string]interface{})
	for _, field := range []string{"id", "event_type", "instance_id", "user", "details", "created_at"} {
		if _, ok := first[field]; !ok {
			t.Errorf("entry missing field %q", field)
		}
	}
}

func TestGetAuditLogs_FilterByInstanceID(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	AuditLog.LogConnection(1, "admin", "connect 1")
	AuditLog.LogConnection(2, "admin", "connect 2")
	AuditLog.LogConnection(1, "admin", "connect 1 again")

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs?instance_id=1", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	if total := result["total"].(float64); total != 2 {
		t.Errorf("expected total 2, got %.0f", total)
	}
}

func TestGetAuditLogs_FilterByEventType(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	AuditLog.LogConnection(1, "admin", "connect")
	AuditLog.LogDisconnection(1, "admin", "disconnect")
	AuditLog.LogCommandExec(1, "admin", "echo hello")

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs?event_type=connection", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	if total := result["total"].(float64); total != 1 {
		t.Errorf("expected total 1, got %.0f", total)
	}
}

func TestGetAuditLogs_Pagination(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	for i := 0; i < 15; i++ {
		AuditLog.LogConnection(1, "admin", fmt.Sprintf("connect %d", i))
	}

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs?limit=5&offset=0", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	entries := result["entries"].([]interface{})
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
	if total := result["total"].(float64); total != 15 {
		t.Errorf("expected total 15, got %.0f", total)
	}

	// Second page
	req2 := buildRequest(t, "GET", "/api/v1/audit-logs?limit=5&offset=5", user, nil)
	w2 := httptest.NewRecorder()
	GetAuditLogs(w2, req2)

	var result2 map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&result2)
	entries2 := result2["entries"].([]interface{})
	if len(entries2) != 5 {
		t.Fatalf("expected 5 entries on page 2, got %d", len(entries2))
	}
}

func TestGetAuditLogs_InvalidInstanceID(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs?instance_id=abc", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetAuditLogs_InvalidLimit(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs?limit=-1", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetAuditLogs_InvalidOffset(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs?offset=-1", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetAuditLogs_LimitCappedAt1000(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs?limit=5000", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	// Should succeed (limit silently capped)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetAuditLogs_NotInitialized(t *testing.T) {
	setupTestDB(t)
	AuditLog = nil

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetAuditLogs_ResponseFormat(t *testing.T) {
	cleanup := setupAuditTest(t)
	defer cleanup()

	AuditLog.LogConnection(1, "admin", "test")

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/audit-logs", user, nil)
	w := httptest.NewRecorder()

	GetAuditLogs(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	if _, ok := result["entries"]; !ok {
		t.Error("response missing 'entries' field")
	}
	if _, ok := result["total"]; !ok {
		t.Error("response missing 'total' field")
	}
}
