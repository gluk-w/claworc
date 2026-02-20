package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
)

func TestIsTunnelConnected_NilManager(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = nil

	if isTunnelConnected(1) {
		t.Error("expected false when Manager is nil")
	}
}

func TestIsTunnelConnected_NoClient(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()

	if isTunnelConnected(999) {
		t.Error("expected false when no client exists")
	}
}

func TestIsTunnelConnected_HealthySession(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()

	cli, _ := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(1, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(1, tc)

	if !isTunnelConnected(1) {
		t.Error("expected true for a healthy tunnel")
	}
}

func TestIsTunnelConnected_ClosedSession(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()

	cli, _ := newYamuxPair(t)
	tc := tunnel.NewTunnelClient(2, "test")
	tc.SetSession(cli)
	tunnel.Manager.Set(2, tc)

	cli.Close()

	if isTunnelConnected(2) {
		t.Error("expected false for a closed tunnel")
	}
}

func TestIsTunnelConnected_NilSession(t *testing.T) {
	old := tunnel.Manager
	defer func() { tunnel.Manager = old }()
	tunnel.Manager = tunnel.NewTunnelManager()

	// TunnelClient with no session set (default nil)
	tc := tunnel.NewTunnelClient(3, "test")
	tunnel.Manager.Set(3, tc)

	if isTunnelConnected(3) {
		t.Error("expected false for a nil session")
	}
}

// TestTunnelStatus_NoDB tests the handler when no DB is configured.
// Since TunnelStatus depends on the database for instance lookup, we test
// the handler path that doesn't require DB by using isTunnelConnected directly
// and testing the HTTP handler response format via a tunnel-status-like request.
// Full handler integration tests require DB setup which is outside unit test scope.
func TestTunnelStatus_InvalidID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/instances/abc/tunnel-status", nil)
	req = withChiParams(req, map[string]string{"id": "abc"})

	TunnelStatus(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["detail"] != "Invalid instance ID" {
		t.Errorf("unexpected detail: %s", resp["detail"])
	}
}
