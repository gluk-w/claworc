package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// nonLegacyInstance creates a running instance on the on-demand browser
// layout. createTestInstance leaves ContainerImage empty, which
// IsLegacyEmbedded treats as legacy, so tests for the browser_enabled gate
// must set a real agent image.
func nonLegacyInstance(t *testing.T, name, display string) database.Instance {
	t.Helper()
	inst := createTestInstance(t, name, display)
	if err := database.DB.Model(&inst).Update("container_image", "claworc/openclaw:latest").Error; err != nil {
		t.Fatalf("set container image: %v", err)
	}
	inst.ContainerImage = "claworc/openclaw:latest"
	return inst
}

// buildJSONRequest is buildRequest plus a JSON body.
func buildJSONRequest(t *testing.T, method, url string, user *database.User, chiParams map[string]string, body string) *http.Request {
	t.Helper()
	req := buildRequest(t, method, url, user, chiParams)
	req.Body = io.NopCloser(bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// stubBridge satisfies the handlers.BrowserBridge interface so BrowserStatus
// takes the non-nil-bridge path; none of its methods should be reached by
// these tests.
type stubBridge struct{}

func (stubBridge) EnsureSession(context.Context, uint, uint) error { return nil }
func (stubBridge) DialCDP(context.Context, uint) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("stub")
}
func (stubBridge) DialVNC(context.Context, uint) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("stub")
}
func (stubBridge) VNCDialer(context.Context, uint) (func(context.Context, string, string) (net.Conn, error), error) {
	return nil, fmt.Errorf("stub")
}
func (stubBridge) TestConnection(context.Context, uint) (string, error) { return "", nil }
func (stubBridge) Reconnect(context.Context, uint) error               { return nil }
func (stubBridge) Touch(uint)                                          {}

func TestSetBrowserEnabled_DisableAndReenable(t *testing.T) {
	setupTestDB(t)
	inst := nonLegacyInstance(t, "bot-ben1", "Ben1")
	user := createTestUser(t, "admin")

	// Disable.
	w := httptest.NewRecorder()
	SetBrowserEnabled(w, buildJSONRequest(t, "PATCH", "/api/v1/instances/{id}/browser-enabled", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID)}, `{"browser_enabled": false}`))
	if w.Code != http.StatusOK {
		t.Fatalf("disable: status %d body=%s", w.Code, w.Body.String())
	}
	var row database.Instance
	if err := database.DB.First(&row, inst.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.BrowserEnabled {
		t.Errorf("BrowserEnabled = true after disable, want false")
	}

	// Re-enable.
	w = httptest.NewRecorder()
	SetBrowserEnabled(w, buildJSONRequest(t, "PATCH", "/api/v1/instances/{id}/browser-enabled", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID)}, `{"browser_enabled": true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("enable: status %d body=%s", w.Code, w.Body.String())
	}
	if err := database.DB.First(&row, inst.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !row.BrowserEnabled {
		t.Errorf("BrowserEnabled = false after re-enable, want true")
	}
}

func TestSetBrowserEnabled_LegacyRejected(t *testing.T) {
	setupTestDB(t)
	// Empty container image → legacy embedded layout; the gate doesn't apply.
	inst := createTestInstance(t, "bot-ben2", "Ben2")
	user := createTestUser(t, "admin")

	w := httptest.NewRecorder()
	SetBrowserEnabled(w, buildJSONRequest(t, "PATCH", "/api/v1/instances/{id}/browser-enabled", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID)}, `{"browser_enabled": false}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestSetBrowserEnabled_MissingBodyRejected(t *testing.T) {
	setupTestDB(t)
	inst := nonLegacyInstance(t, "bot-ben3", "Ben3")
	user := createTestUser(t, "admin")

	w := httptest.NewRecorder()
	SetBrowserEnabled(w, buildJSONRequest(t, "PATCH", "/api/v1/instances/{id}/browser-enabled", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID)}, `{}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// TestBrowserStatus_DisabledGate verifies the per-instance gate is what emits
// "disabled" — the bridge is present (stub), so without the gate the state
// would be "stopped".
func TestBrowserStatus_DisabledGate(t *testing.T) {
	setupTestDB(t)
	// setupTestDB migrates only the core tables; the enabled/"stopped" branch
	// of BrowserStatus queries browser_sessions.
	if err := database.DB.AutoMigrate(&database.BrowserSession{}); err != nil {
		t.Fatalf("migrate browser_sessions: %v", err)
	}
	prev := BrowserBridgeRef
	BrowserBridgeRef = stubBridge{}
	t.Cleanup(func() { BrowserBridgeRef = prev })

	inst := nonLegacyInstance(t, "bot-ben4", "Ben4")
	user := createTestUser(t, "admin")
	req := func() *http.Request {
		return buildRequest(t, "GET", "/api/v1/instances/{id}/browser/status", user,
			map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	}

	// Enabled (default): no session row → "stopped".
	w := httptest.NewRecorder()
	BrowserStatus(w, req())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if got := parseResponse(t, w)["state"]; got != "stopped" {
		t.Errorf("state = %v, want stopped", got)
	}

	// Disabled → "disabled".
	if err := database.DB.Model(&inst).Update("browser_enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	w = httptest.NewRecorder()
	BrowserStatus(w, req())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if got := parseResponse(t, w)["state"]; got != "disabled" {
		t.Errorf("state = %v, want disabled", got)
	}
}

// TestDesktopProxy_DisabledReturns409 verifies the desktop proxy refuses with
// a non-retryable status instead of trying to spawn a session.
func TestDesktopProxy_DisabledReturns409(t *testing.T) {
	setupTestDB(t)
	inst := nonLegacyInstance(t, "bot-ben5", "Ben5")
	if err := database.DB.Model(&inst).Update("browser_enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	user := createTestUser(t, "admin")

	w := httptest.NewRecorder()
	DesktopProxy(w, buildRequest(t, "GET", "/api/v1/instances/{id}/desktop/websockify", user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID), "*": "websockify"}))
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409; body=%s", w.Code, w.Body.String())
	}
}

// TestCloneInstance_CopiesBrowserEnabled verifies the GORM default:true
// workaround: a disabled source must produce a disabled clone.
func TestCloneInstance_CopiesBrowserEnabled(t *testing.T) {
	cloneSetup(t)

	src := nonLegacyInstance(t, "bot-ben6", "Ben6")
	if err := database.DB.Model(&src).Update("browser_enabled", false).Error; err != nil {
		t.Fatalf("seed src: %v", err)
	}
	user := createTestUser(t, "admin")

	w := httptest.NewRecorder()
	CloneInstance(w, reqClone(t, src.ID, user))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	dstID := uint(parseResponse(t, w)["id"].(float64))

	var dst database.Instance
	if err := database.DB.First(&dst, dstID).Error; err != nil {
		t.Fatalf("load dst: %v", err)
	}
	if dst.BrowserEnabled {
		t.Errorf("BrowserEnabled = true on clone, want false")
	}
}
