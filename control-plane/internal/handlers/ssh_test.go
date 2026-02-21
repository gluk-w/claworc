package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- test SSH server (mirrors sshproxy test helpers) ---

func testSSHServer(t *testing.T, authorizedKey ssh.PublicKey) (string, func()) {
	t.Helper()

	_, hostKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(authorizedKey) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleTestConn(netConn, cfg)
		}
	}()

	cleanup := func() {
		listener.Close()
		<-done
	}
	return listener.Addr().String(), cleanup
}

func handleTestConn(netConn net.Conn, cfg *ssh.ServerConfig) {
	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, cfg)
	if err != nil {
		netConn.Close()
		return
	}
	defer sshConn.Close()

	go func() {
		for req := range reqs {
			if req.WantReply {
				req.Reply(true, nil)
			}
		}
	}()

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				if req.Type == "exec" {
					ch.Write([]byte("SSH test successful\n"))
					ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					if req.WantReply {
						req.Reply(true, nil)
					}
					return
				}
				if req.WantReply {
					req.Reply(true, nil)
				}
			}
		}()
	}
}

// --- mock orchestrator ---

type mockOrchestrator struct {
	sshHost string
	sshPort int

	configureErr error
	addressErr   error
}

func (m *mockOrchestrator) Initialize(_ context.Context) error { return nil }
func (m *mockOrchestrator) IsAvailable(_ context.Context) bool { return true }
func (m *mockOrchestrator) BackendName() string                { return "mock" }
func (m *mockOrchestrator) CreateInstance(_ context.Context, _ orchestrator.CreateParams) error {
	return nil
}
func (m *mockOrchestrator) DeleteInstance(_ context.Context, _ string) error  { return nil }
func (m *mockOrchestrator) StartInstance(_ context.Context, _ string) error   { return nil }
func (m *mockOrchestrator) StopInstance(_ context.Context, _ string) error    { return nil }
func (m *mockOrchestrator) RestartInstance(_ context.Context, _ string) error { return nil }
func (m *mockOrchestrator) GetInstanceStatus(_ context.Context, _ string) (string, error) {
	return "running", nil
}
func (m *mockOrchestrator) UpdateInstanceConfig(_ context.Context, _, _ string) error { return nil }
func (m *mockOrchestrator) StreamInstanceLogs(_ context.Context, _ string, _ int, _ bool) (<-chan string, error) {
	return nil, nil
}
func (m *mockOrchestrator) CloneVolumes(_ context.Context, _, _ string) error { return nil }
func (m *mockOrchestrator) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error {
	return m.configureErr
}
func (m *mockOrchestrator) GetSSHAddress(_ context.Context, _ uint) (string, int, error) {
	if m.addressErr != nil {
		return "", 0, m.addressErr
	}
	return m.sshHost, m.sshPort, nil
}
func (m *mockOrchestrator) ExecInInstance(_ context.Context, _ string, _ []string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockOrchestrator) ExecInteractive(_ context.Context, _ string, _ []string) (*orchestrator.ExecSession, error) {
	return nil, nil
}
func (m *mockOrchestrator) ListDirectory(_ context.Context, _ string, _ string) ([]orchestrator.FileEntry, error) {
	return nil, nil
}
func (m *mockOrchestrator) ReadFile(_ context.Context, _ string, _ string) ([]byte, error) {
	return nil, nil
}
func (m *mockOrchestrator) CreateFile(_ context.Context, _, _, _ string) error { return nil }
func (m *mockOrchestrator) CreateDirectory(_ context.Context, _, _ string) error {
	return nil
}
func (m *mockOrchestrator) WriteFile(_ context.Context, _ string, _ string, _ []byte) error {
	return nil
}
func (m *mockOrchestrator) GetVNCBaseURL(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (m *mockOrchestrator) GetGatewayWSURL(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *mockOrchestrator) GetHTTPTransport() http.RoundTripper { return nil }

// --- test helpers ---

func setupTestDB(t *testing.T) {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	database.DB.AutoMigrate(&database.Instance{}, &database.Setting{}, &database.User{}, &database.UserInstance{}, &database.InstanceAPIKey{})
}

func createTestInstance(t *testing.T, name, displayName string) database.Instance {
	t.Helper()
	inst := database.Instance{
		Name:        name,
		DisplayName: displayName,
		Status:      "running",
	}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create test instance: %v", err)
	}
	return inst
}

func createTestUser(t *testing.T, role string) *database.User {
	t.Helper()
	user := &database.User{
		Username:     "testuser",
		PasswordHash: "unused",
		Role:         role,
	}
	if err := database.DB.Create(user).Error; err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return user
}

// buildRequest creates an HTTP request with chi URL params and an authenticated user in context.
func buildRequest(t *testing.T, method, url string, user *database.User, chiParams map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)

	// Set chi URL params via a route context
	rctx := chi.NewRouteContext()
	for k, v := range chiParams {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)

	// Set user in context for middleware.CanAccessInstance
	if user != nil {
		ctx = middleware.WithUser(ctx, user)
	}

	return req.WithContext(ctx)
}

func parseResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	body, err := io.ReadAll(w.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, string(body))
	}
	return result
}

// --- tests ---

func TestSSHConnectionTest_Success(t *testing.T) {
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	mock := &mockOrchestrator{sshHost: host, sshPort: port}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/ssh-test", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", result["status"])
	}
	if output, ok := result["output"].(string); !ok || output == "" {
		t.Errorf("expected non-empty output, got %v", result["output"])
	}
	if result["error"] != nil {
		t.Errorf("expected nil error, got %v", result["error"])
	}
	if latency, ok := result["latency_ms"].(float64); !ok || latency < 0 {
		t.Errorf("expected non-negative latency_ms, got %v", result["latency_ms"])
	}
}

func TestSSHConnectionTest_InstanceNotFound(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/999/ssh-test", user, map[string]string{"id": "999"})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "Instance not found" {
		t.Errorf("expected 'Instance not found', got %v", result["detail"])
	}
}

func TestSSHConnectionTest_Forbidden(t *testing.T) {
	setupTestDB(t)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned to this instance

	req := buildRequest(t, "GET", "/api/v1/instances/1/ssh-test", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "Access denied" {
		t.Errorf("expected 'Access denied', got %v", result["detail"])
	}
}

func TestSSHConnectionTest_NoOrchestrator(t *testing.T) {
	setupTestDB(t)

	orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	SSHMgr = sshproxy.NewSSHManager(nil, "")

	req := buildRequest(t, "GET", "/api/v1/instances/1/ssh-test", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["error"] != "No orchestrator available" {
		t.Errorf("expected 'No orchestrator available', got %v", result["error"])
	}
}

func TestSSHConnectionTest_ConnectionFailure(t *testing.T) {
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	// Point to a port that is not listening
	mock := &mockOrchestrator{
		addressErr: fmt.Errorf("instance not running"),
	}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/ssh-test", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 (with error payload), got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["status"] != "error" {
		t.Errorf("expected status 'error', got %v", result["status"])
	}
	if result["error"] == nil || result["error"] == "" {
		t.Errorf("expected non-empty error field, got %v", result["error"])
	}
}

func TestSSHConnectionTest_ResponseFormat(t *testing.T) {
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	mock := &mockOrchestrator{sshHost: host, sshPort: port}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/ssh-test", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	result := parseResponse(t, w)

	// Verify all expected fields exist
	for _, key := range []string{"status", "output", "latency_ms", "error"} {
		if _, exists := result[key]; !exists {
			t.Errorf("response missing field %q", key)
		}
	}
}

func TestSSHConnectionTest_InvalidID(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/notanumber/ssh-test", user, map[string]string{"id": "notanumber"})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestSSHConnectionTest_NoSSHManager(t *testing.T) {
	setupTestDB(t)

	mock := &mockOrchestrator{sshHost: "127.0.0.1", sshPort: 22}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	SSHMgr = nil

	req := buildRequest(t, "GET", "/api/v1/instances/1/ssh-test", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	SSHConnectionTest(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["error"] != "SSH manager not initialized" {
		t.Errorf("expected 'SSH manager not initialized', got %v", result["error"])
	}
}

// --- GetTunnelStatus tests ---

func TestGetTunnelStatus_NoTunnels(t *testing.T) {
	setupTestDB(t)

	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	SSHMgr = sshproxy.NewSSHManager(signer, "")
	TunnelMgr = sshproxy.NewTunnelManager(SSHMgr)
	defer SSHMgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/tunnels", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	result := parseResponse(t, w)
	tunnels, ok := result["tunnels"].([]interface{})
	if !ok {
		t.Fatalf("expected tunnels to be an array, got %T", result["tunnels"])
	}
	if len(tunnels) != 0 {
		t.Errorf("expected 0 tunnels, got %d", len(tunnels))
	}
}

func TestGetTunnelStatus_MultipleTunnels(t *testing.T) {
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")

	// Connect SSH and create tunnels
	_, err = mgr.Connect(context.Background(), inst.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(mgr)
	TunnelMgr = tm

	_, err = tm.CreateTunnelForVNC(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("create VNC tunnel: %v", err)
	}
	_, err = tm.CreateTunnelForGateway(context.Background(), inst.ID, 0)
	if err != nil {
		t.Fatalf("create Gateway tunnel: %v", err)
	}

	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/tunnels", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	result := parseResponse(t, w)
	tunnels, ok := result["tunnels"].([]interface{})
	if !ok {
		t.Fatalf("expected tunnels to be an array, got %T", result["tunnels"])
	}
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels, got %d", len(tunnels))
	}

	// Verify response fields for each tunnel
	labels := map[string]bool{}
	for _, raw := range tunnels {
		tun, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected tunnel to be an object, got %T", raw)
		}

		// Verify all expected fields are present
		for _, field := range []string{"label", "type", "local_port", "remote_port", "status", "last_check"} {
			if _, exists := tun[field]; !exists {
				t.Errorf("tunnel missing field %q", field)
			}
		}

		label, _ := tun["label"].(string)
		labels[label] = true

		if tun["type"] != "reverse" {
			t.Errorf("expected type 'reverse', got %v", tun["type"])
		}
		if tun["status"] != "active" {
			t.Errorf("expected status 'active', got %v", tun["status"])
		}
		if localPort, ok := tun["local_port"].(float64); !ok || localPort == 0 {
			t.Errorf("expected non-zero local_port, got %v", tun["local_port"])
		}
	}

	if !labels["VNC"] {
		t.Error("missing VNC tunnel in response")
	}
	if !labels["Gateway"] {
		t.Error("missing Gateway tunnel in response")
	}
}

func TestGetTunnelStatus_ResponseFormat(t *testing.T) {
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey())
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")

	_, err = mgr.Connect(context.Background(), inst.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	tm := sshproxy.NewTunnelManager(mgr)
	TunnelMgr = tm

	_, err = tm.CreateTunnelForVNC(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("create VNC tunnel: %v", err)
	}

	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/tunnels", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	result := parseResponse(t, w)
	tunnels := result["tunnels"].([]interface{})
	tun := tunnels[0].(map[string]interface{})

	// Verify VNC tunnel details
	if tun["label"] != "VNC" {
		t.Errorf("expected label 'VNC', got %v", tun["label"])
	}
	if tun["type"] != "reverse" {
		t.Errorf("expected type 'reverse', got %v", tun["type"])
	}
	if remotePort, ok := tun["remote_port"].(float64); !ok || int(remotePort) != 3000 {
		t.Errorf("expected remote_port 3000, got %v", tun["remote_port"])
	}
	if tun["status"] != "active" {
		t.Errorf("expected status 'active', got %v", tun["status"])
	}
	// last_check should be a valid RFC3339 string
	lastCheck, ok := tun["last_check"].(string)
	if !ok || lastCheck == "" {
		t.Errorf("expected non-empty last_check string, got %v", tun["last_check"])
	}
}

func TestGetTunnelStatus_Forbidden(t *testing.T) {
	setupTestDB(t)

	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	SSHMgr = sshproxy.NewSSHManager(signer, "")
	TunnelMgr = sshproxy.NewTunnelManager(SSHMgr)
	defer SSHMgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned

	req := buildRequest(t, "GET", "/api/v1/instances/1/tunnels", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "Access denied" {
		t.Errorf("expected 'Access denied', got %v", result["detail"])
	}
}

func TestGetTunnelStatus_InstanceNotFound(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/999/tunnels", user, map[string]string{"id": "999"})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "Instance not found" {
		t.Errorf("expected 'Instance not found', got %v", result["detail"])
	}
}

func TestGetTunnelStatus_InvalidID(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "GET", "/api/v1/instances/notanumber/tunnels", user, map[string]string{"id": "notanumber"})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestGetTunnelStatus_NoTunnelManager(t *testing.T) {
	setupTestDB(t)

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	TunnelMgr = nil

	req := buildRequest(t, "GET", "/api/v1/instances/1/tunnels", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	GetTunnelStatus(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["error"] != "Tunnel manager not initialized" {
		t.Errorf("expected 'Tunnel manager not initialized', got %v", result["error"])
	}
}
