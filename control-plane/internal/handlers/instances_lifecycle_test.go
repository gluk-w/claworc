package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

func TestStopInstance_StopsTunnels(t *testing.T) {
	setupTestDB(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-stop", "Stop Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/stop", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	StopInstance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["status"] != "stopping" {
		t.Errorf("expected status 'stopping', got %v", result["status"])
	}

	// Verify tunnels were cleaned up (should be empty since none were created)
	tunnels := tm.GetTunnelsForInstance(inst.ID)
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after stop, got %d", len(tunnels))
	}
}

func TestStopInstance_NilTunnelMgr(t *testing.T) {
	setupTestDB(t)

	TunnelMgr = nil

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-stop", "Stop Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/stop", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	StopInstance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestDeleteInstance_StopsTunnels(t *testing.T) {
	setupTestDB(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-del", "Delete Test")

	req := buildRequest(t, "DELETE", "/api/v1/instances/1", nil, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	DeleteInstance(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Verify tunnels were cleaned up
	tunnels := tm.GetTunnelsForInstance(inst.ID)
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after delete, got %d", len(tunnels))
	}
}

func TestDeleteInstance_NilTunnelMgr(t *testing.T) {
	setupTestDB(t)

	TunnelMgr = nil

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-del", "Delete Test")

	req := buildRequest(t, "DELETE", "/api/v1/instances/1", nil, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	DeleteInstance(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestRestartInstance_StopsTunnels(t *testing.T) {
	setupTestDB(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-restart", "Restart Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/restart", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	RestartInstance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["status"] != "restarting" {
		t.Errorf("expected status 'restarting', got %v", result["status"])
	}

	// Verify tunnels were cleaned up (will be recreated by background manager)
	tunnels := tm.GetTunnelsForInstance(inst.ID)
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels after restart, got %d", len(tunnels))
	}
}

func TestRestartInstance_NilTunnelMgr(t *testing.T) {
	setupTestDB(t)

	TunnelMgr = nil

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-restart", "Restart Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/restart", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	RestartInstance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestStartInstance_NoTunnelCleanup(t *testing.T) {
	setupTestDB(t)

	sshMgr := sshproxy.NewSSHManager(nil, "")
	tm := sshproxy.NewTunnelManager(sshMgr)
	TunnelMgr = tm
	defer func() { TunnelMgr = nil }()

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-start", "Start Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/start", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	StartInstance(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["status"] != "running" {
		t.Errorf("expected status 'running', got %v", result["status"])
	}
}
