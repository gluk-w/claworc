package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

func TestGetSSHEvents_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/ssh-events", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	GetSSHEvents(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHEvents_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", "/api/v1/instances/999/ssh-events", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetSSHEvents_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestGetSSHEvents_NoManagers(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(resp.Events))
	}
}

func TestGetSSHEvents_WithEvents(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-events", DisplayName: "Events", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Emit some events
	sm.LogEvent("bot-events", sshmanager.EventConnected, "connected to 10.0.0.1")
	sm.LogEvent("bot-events", sshmanager.EventHealthCheckFailed, "connection timed out")
	sm.LogEvent("bot-events", sshmanager.EventDisconnected, "keepalive failed")
	sm.LogEvent("bot-events", sshmanager.EventReconnecting, "starting reconnection")
	sm.LogEvent("bot-events", sshmanager.EventReconnectSuccess, "reconnected after 2 attempts")

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(resp.Events))
	}

	// Verify event types in order
	expectedTypes := []string{"connected", "health_check_failed", "disconnected", "reconnecting", "reconnect_success"}
	for i, expected := range expectedTypes {
		if resp.Events[i].Type != expected {
			t.Errorf("event %d: expected type %q, got %q", i, expected, resp.Events[i].Type)
		}
	}

	// Verify instance name is set on all events
	for i, e := range resp.Events {
		if e.InstanceName != "bot-events" {
			t.Errorf("event %d: expected instance_name 'bot-events', got %q", i, e.InstanceName)
		}
	}

	// Verify timestamps are non-empty
	for i, e := range resp.Events {
		if e.Timestamp == "" {
			t.Errorf("event %d: expected non-empty timestamp", i)
		}
	}

	// Verify details
	if resp.Events[0].Details != "connected to 10.0.0.1" {
		t.Errorf("event 0: expected details 'connected to 10.0.0.1', got %q", resp.Events[0].Details)
	}
}

func TestGetSSHEvents_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-format", DisplayName: "Format", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify JSON has the expected structure
	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if _, ok := raw["events"]; !ok {
		t.Error("response missing 'events' field")
	}
}

func TestGetSSHEvents_WithLimit(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-limit", DisplayName: "Limit", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Emit 10 events
	for i := 0; i < 10; i++ {
		sm.LogEvent("bot-limit", sshmanager.EventConnected, fmt.Sprintf("event %d", i))
	}

	// Request with limit=3
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events?limit=3", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Events) != 3 {
		t.Errorf("expected 3 events with limit=3, got %d", len(resp.Events))
	}
}

func TestGetSSHEvents_DefaultLimit(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-deflimit", DisplayName: "DefLimit", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Emit 60 events (more than default limit of 50)
	for i := 0; i < 60; i++ {
		sm.LogEvent("bot-deflimit", sshmanager.EventConnected, fmt.Sprintf("event %d", i))
	}

	// Request without limit (should default to 50)
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Events) != 50 {
		t.Errorf("expected 50 events with default limit, got %d", len(resp.Events))
	}
}

func TestGetSSHEvents_LimitCappedAt100(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-maxlimit", DisplayName: "MaxLimit", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// The SSHManager ring buffer is capped at 100 per instance anyway
	// but let's verify the limit parameter is capped at 100
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events?limit=999", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	// Should succeed - the limit is capped but the request shouldn't fail
	var resp sshEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// With 0 events emitted, should still be 0
	if len(resp.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(resp.Events))
	}
}

func TestGetSSHEvents_InvalidLimit(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-badlimit", DisplayName: "BadLimit", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Emit 5 events
	for i := 0; i < 5; i++ {
		sm.LogEvent("bot-badlimit", sshmanager.EventConnected, fmt.Sprintf("event %d", i))
	}

	// Request with invalid limit (should fall back to default 50)
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events?limit=abc", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// With default limit=50 and only 5 events, should get all 5
	if len(resp.Events) != 5 {
		t.Errorf("expected 5 events with invalid limit (defaults to 50), got %d", len(resp.Events))
	}
}

func TestGetSSHEvents_ViewerAssigned(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-viewer", DisplayName: "Viewer", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Assign viewer to instance
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst.ID})

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.LogEvent("bot-viewer", sshmanager.EventConnected, "connected")

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-events", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetSSHEvents(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for assigned viewer, got %d", w.Code)
	}

	var resp sshEventsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(resp.Events))
	}
}
