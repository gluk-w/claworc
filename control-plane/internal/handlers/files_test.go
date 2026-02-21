package handlers

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	gossh "golang.org/x/crypto/ssh"
)

// --- Test SSH server for file handler tests ---

type fileCommandHandler func(cmd string, stdin io.Reader) (stdout, stderr string, exitCode int)

func startFileTestSSHServer(t *testing.T, handler fileCommandHandler) (*gossh.Client, func()) {
	t.Helper()

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientSSHPub, err := gossh.NewPublicKey(clientPub)
	if err != nil {
		t.Fatalf("convert client pub key: %v", err)
	}

	serverCfg := &gossh.ServerConfig{
		PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			if bytes.Equal(key.Marshal(), clientSSHPub.Marshal()) {
				return &gossh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	serverCfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleFileTestSSHConn(conn, serverCfg, handler)
		}
	}()

	clientSigner, err := gossh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatalf("create client signer: %v", err)
	}

	clientCfg := &gossh.ClientConfig{
		User:            "root",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(clientSigner)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	sshClient, err := gossh.Dial("tcp", listener.Addr().String(), clientCfg)
	if err != nil {
		listener.Close()
		t.Fatalf("dial SSH: %v", err)
	}

	return sshClient, func() {
		sshClient.Close()
		listener.Close()
	}
}

func handleFileTestSSHConn(netConn net.Conn, config *gossh.ServerConfig, handler fileCommandHandler) {
	defer netConn.Close()
	srvConn, chans, reqs, err := gossh.NewServerConn(netConn, config)
	if err != nil {
		return
	}
	defer srvConn.Close()
	go gossh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(gossh.UnknownChannelType, "unsupported channel type")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleFileTestSession(ch, requests, handler)
	}
}

func handleFileTestSession(ch gossh.Channel, reqs <-chan *gossh.Request, handler fileCommandHandler) {
	defer ch.Close()

	for req := range reqs {
		switch req.Type {
		case "exec":
			if len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			cmdLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
			if len(req.Payload) < 4+cmdLen {
				req.Reply(false, nil)
				continue
			}
			cmd := string(req.Payload[4 : 4+cmdLen])
			req.Reply(true, nil)

			stdout, stderr, exitCode := handler(cmd, ch)

			if stdout != "" {
				ch.Write([]byte(stdout))
			}
			if stderr != "" {
				ch.Stderr().Write([]byte(stderr))
			}

			exitPayload := gossh.Marshal(struct{ Status uint32 }{uint32(exitCode)})
			ch.SendRequest("exit-status", false, exitPayload)
			return

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// --- Helper: set up SSHManager with a test client for a given instance ---

func setupSSHManagerWithClient(t *testing.T, instanceName string, client *gossh.Client) func() {
	t.Helper()
	sm := sshmanager.NewSSHManager(0)
	sm.SetClient(instanceName, client)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	return func() {
		sshtunnel.ResetGlobalForTest()
	}
}

func setupSSHManagerEmpty(t *testing.T) func() {
	t.Helper()
	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	return func() {
		sshtunnel.ResetGlobalForTest()
	}
}

func createTestInstance(t *testing.T, name, displayName string) database.Instance {
	t.Helper()
	inst := database.Instance{Name: name, DisplayName: displayName, Status: "running"}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return inst
}

func createTestAdmin(t *testing.T) *database.User {
	t.Helper()
	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	if err := database.DB.Create(admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return admin
}

// ========================================================================
// BrowseFiles tests
// ========================================================================

func TestBrowseFiles_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/files", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBrowseFiles_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/999/files", map[string]string{"id": "999"})
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestBrowseFiles_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse", "Browse")
	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestBrowseFiles_NoSSHManager(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse-nossh", "Browse NoSSH")
	admin := createTestAdmin(t)

	// Don't set up SSHManager — leave it nil
	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestBrowseFiles_NoSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse-noconn", "Browse NoConn")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestBrowseFiles_DefaultPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse-default", "Browse Default")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "ls -la") && strings.Contains(cmd, "/root") {
			return "total 4\ndrwxr-xr-x 2 root root 4096 Jan  1 00:00 .\ndrwxr-xr-x 3 root root 4096 Jan  1 00:00 ..\n-rw-r--r-- 1 root root  100 Jan  1 00:00 test.txt\n", "", 0
		}
		return "", "unexpected command", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-browse-default", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["path"] != "/root" {
		t.Errorf("expected path /root, got %v", resp["path"])
	}
}

func TestBrowseFiles_CustomPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse-custom", "Browse Custom")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "/tmp") {
			return "total 0\ndrwxrwxrwt 2 root root 4096 Jan  1 00:00 .\ndrwxr-xr-x 3 root root 4096 Jan  1 00:00 ..\n", "", 0
		}
		return "", "unexpected command", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-browse-custom", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files?path=/tmp", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["path"] != "/tmp" {
		t.Errorf("expected path /tmp, got %v", resp["path"])
	}
}

func TestBrowseFiles_SSHError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse-err", "Browse Err")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "ls: cannot access '/nonexistent': No such file or directory", 2
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-browse-err", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files?path=/nonexistent", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ========================================================================
// ReadFileContent tests
// ========================================================================

func TestReadFileContent_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/files/read", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestReadFileContent_MissingPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-read-nopath", "Read NoPath")
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/read", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestReadFileContent_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/999/files/read?path=/etc/hosts", map[string]string{"id": "999"})
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestReadFileContent_NoSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-read-noconn", "Read NoConn")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/read?path=/etc/hosts", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestReadFileContent_Success(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-read-ok", "Read OK")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") && strings.Contains(cmd, "/etc/hosts") {
			return "127.0.0.1 localhost\n", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-read-ok", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/read?path=/etc/hosts", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["content"] != "127.0.0.1 localhost\n" {
		t.Errorf("unexpected content: %q", resp["content"])
	}
	if resp["path"] != "/etc/hosts" {
		t.Errorf("expected path /etc/hosts, got %v", resp["path"])
	}
}

// ========================================================================
// DownloadFile tests
// ========================================================================

func TestDownloadFile_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/files/download", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()
	DownloadFile(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDownloadFile_MissingPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-dl-nopath", "DL NoPath")
	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/download", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	w := httptest.NewRecorder()
	DownloadFile(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDownloadFile_Success(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-dl-ok", "DL OK")
	admin := createTestAdmin(t)

	fileContent := "#!/bin/bash\necho hello\n"
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") && strings.Contains(cmd, "/root/script.sh") {
			return fileContent, "", 0
		}
		return "", "not found", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-dl-ok", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/download?path=/root/script.sh", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	DownloadFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "script.sh") {
		t.Errorf("expected Content-Disposition with script.sh, got %q", cd)
	}
	if w.Body.String() != fileContent {
		t.Errorf("unexpected body: %q", w.Body.String())
	}
}

// ========================================================================
// CreateNewFile tests
// ========================================================================

func TestCreateNewFile_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("POST", "/api/v1/instances/abc/files", map[string]string{"id": "abc"})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/test.txt","content":"hello"}`))
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateNewFile_InvalidBody(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-create-bad", "Create Bad")
	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{invalid`))
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateNewFile_NoSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-create-noconn", "Create NoConn")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/test.txt","content":"hello"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestCreateNewFile_Success(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-create-ok", "Create OK")
	admin := createTestAdmin(t)

	var writtenData string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") && strings.Contains(cmd, "/root/new.txt") {
			data, _ := io.ReadAll(stdin)
			writtenData = string(data)
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-create-ok", sshClient)
	defer smCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/new.txt","content":"hello world"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
	if resp["path"] != "/root/new.txt" {
		t.Errorf("expected path /root/new.txt, got %v", resp["path"])
	}
	if writtenData != "hello world" {
		t.Errorf("expected written data 'hello world', got %q", writtenData)
	}
}

// ========================================================================
// CreateDirectory tests
// ========================================================================

func TestCreateDirectory_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("POST", "/api/v1/instances/abc/directories", map[string]string{"id": "abc"})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/newdir"}`))
	w := httptest.NewRecorder()
	CreateDirectory(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateDirectory_NoSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-mkdir-noconn", "Mkdir NoConn")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/directories", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/newdir"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateDirectory(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestCreateDirectory_Success(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-mkdir-ok", "Mkdir OK")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "mkdir -p") && strings.Contains(cmd, "/root/newdir") {
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-mkdir-ok", sshClient)
	defer smCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/directories", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/newdir"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateDirectory(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

func TestCreateDirectory_SSHError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-mkdir-err", "Mkdir Err")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "mkdir: cannot create directory: Permission denied", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-mkdir-err", sshClient)
	defer smCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/directories", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/newdir"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateDirectory(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ========================================================================
// UploadFile tests
// ========================================================================

func TestUploadFile_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("POST", "/api/v1/instances/abc/files/upload?path=/root", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUploadFile_MissingPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-nopath", "Upload NoPath")
	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files/upload", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUploadFile_NoSSHConnection(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-noconn", "Upload NoConn")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded content"))
	writer.Close()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files/upload?path=/root", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Body = io.NopCloser(body)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestUploadFile_Success(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-ok", "Upload OK")
	admin := createTestAdmin(t)

	var writtenData string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") && strings.Contains(cmd, "/root/test.txt") {
			data, _ := io.ReadAll(stdin)
			writtenData = string(data)
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-upload-ok", sshClient)
	defer smCleanup()

	// Create multipart form
	mpBody := &bytes.Buffer{}
	writer := multipart.NewWriter(mpBody)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded content"))
	writer.Close()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files/upload?path=/root", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Body = io.NopCloser(mpBody)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
	if resp["filename"] != "test.txt" {
		t.Errorf("expected filename test.txt, got %v", resp["filename"])
	}
	if writtenData != "uploaded content" {
		t.Errorf("expected written 'uploaded content', got %q", writtenData)
	}
}

func TestUploadFile_MissingFormFile(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-nofile", "Upload NoFile")
	admin := createTestAdmin(t)

	sshCleanup := setupSSHManagerEmpty(t)
	defer sshCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files/upload?path=/root", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUploadFile_PathEndsWithFilename(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-fullpath", "Upload FullPath")
	admin := createTestAdmin(t)

	var writtenPath string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") {
			writtenPath = cmd
			io.ReadAll(stdin)
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-upload-fullpath", sshClient)
	defer smCleanup()

	mpBody := &bytes.Buffer{}
	writer := multipart.NewWriter(mpBody)
	part, _ := writer.CreateFormFile("file", "data.csv")
	part.Write([]byte("a,b,c"))
	writer.Close()

	// path already ends with the filename
	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files/upload?path=/root/data.csv", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Body = io.NopCloser(mpBody)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Should use /root/data.csv directly, not /root/data.csv/data.csv
	if !strings.Contains(writtenPath, "/root/data.csv") {
		t.Errorf("expected path to contain /root/data.csv, got %q", writtenPath)
	}
}
