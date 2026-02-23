package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// --- filesystem-aware test SSH server for file handler tests ---

// fileTestFS simulates a simple in-memory filesystem for the test SSH server.
type fileTestFS struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

func newFileTestFS() *fileTestFS {
	return &fileTestFS{
		files: map[string][]byte{
			"/root/hello.txt": []byte("hello world"),
			"/root/data.bin":  {0x00, 0x01, 0x02, 0xFF},
		},
		dirs: map[string]bool{
			"/root": true,
			"/tmp":  true,
		},
	}
}

func (fs *fileTestFS) handleExec(cmd string) (stdout string, exitCode int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	switch {
	case strings.HasPrefix(cmd, "ls -la --color=never "):
		path := fileExtractShellArg(cmd, "ls -la --color=never ")
		return fs.handleLs(path)
	case strings.HasPrefix(cmd, "cat "):
		path := fileExtractShellArg(cmd, "cat ")
		return fs.handleCat(path)
	case strings.HasPrefix(cmd, "mkdir -p "):
		path := fileExtractShellArg(cmd, "mkdir -p ")
		return fs.handleMkdir(path)
	case strings.HasPrefix(cmd, "> "):
		path := fileExtractShellArg(cmd, "> ")
		fs.files[path] = []byte{}
		return "", 0
	case strings.HasPrefix(cmd, "echo '") && strings.Contains(cmd, "| base64 -d >>"):
		return fs.handleBase64Append(cmd)
	default:
		return fmt.Sprintf("unknown command: %s", cmd), 127
	}
}

func (fs *fileTestFS) handleLs(path string) (string, int) {
	if !fs.dirs[path] {
		return fmt.Sprintf("ls: cannot access '%s': No such file or directory", path), 2
	}
	var lines []string
	lines = append(lines, "total 8")
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for fpath, content := range fs.files {
		if strings.HasPrefix(fpath, prefix) && !strings.Contains(fpath[len(prefix):], "/") {
			name := fpath[len(prefix):]
			lines = append(lines, fmt.Sprintf("-rw-r--r-- 1 root root %d Jan  1 00:00 %s", len(content), name))
		}
	}
	for dpath := range fs.dirs {
		if dpath == path {
			continue
		}
		if strings.HasPrefix(dpath, prefix) && !strings.Contains(dpath[len(prefix):], "/") {
			name := dpath[len(prefix):]
			lines = append(lines, fmt.Sprintf("drwxr-xr-x 2 root root 4096 Jan  1 00:00 %s", name))
		}
	}
	return strings.Join(lines, "\n") + "\n", 0
}

func (fs *fileTestFS) handleCat(path string) (string, int) {
	content, ok := fs.files[path]
	if !ok {
		return fmt.Sprintf("cat: %s: No such file or directory", path), 1
	}
	return string(content), 0
}

func (fs *fileTestFS) handleMkdir(path string) (string, int) {
	parts := strings.Split(path, "/")
	for i := 1; i <= len(parts); i++ {
		p := strings.Join(parts[:i], "/")
		if p == "" {
			continue
		}
		fs.dirs[p] = true
	}
	return "", 0
}

func (fs *fileTestFS) handleBase64Append(cmd string) (string, int) {
	start := strings.Index(cmd, "'") + 1
	end := strings.Index(cmd[start:], "'") + start
	b64 := cmd[start:end]

	pathStart := strings.LastIndex(cmd, ">> ") + 3
	path := fileExtractQuotedArg(cmd[pathStart:])

	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Sprintf("base64 decode error: %v", err), 1
	}

	fs.files[path] = append(fs.files[path], decoded...)
	return "", 0
}

func fileExtractShellArg(cmd, prefix string) string {
	rest := strings.TrimPrefix(cmd, prefix)
	return fileExtractQuotedArg(rest)
}

func fileExtractQuotedArg(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "'") {
		return strings.Fields(s)[0]
	}
	var result strings.Builder
	i := 1
	for i < len(s) {
		if s[i] == '\'' {
			if i+3 < len(s) && s[i:i+4] == "'\\''" {
				result.WriteByte('\'')
				i += 4
				continue
			}
			break
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

func fileTestSSHServer(t *testing.T, authorizedKey ssh.PublicKey, fs *fileTestFS) (addr string, cleanup func()) {
	t.Helper()

	_, hostKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(hostKeyPEM)
	if err != nil {
		t.Fatalf("parse host key: %v", err)
	}

	config := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) == ssh.FingerprintSHA256(authorizedKey) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var conns []net.Conn
	var connsMu sync.Mutex

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := listener.Accept()
			if err != nil {
				return
			}
			connsMu.Lock()
			conns = append(conns, netConn)
			connsMu.Unlock()
			go fileHandleTestConn(netConn, config, fs)
		}
	}()

	return listener.Addr().String(), func() {
		listener.Close()
		connsMu.Lock()
		for _, c := range conns {
			c.Close()
		}
		connsMu.Unlock()
		<-done
	}
}

func fileHandleTestConn(netConn net.Conn, config *ssh.ServerConfig, fs *fileTestFS) {
	sshConn, chans, reqs, err := ssh.NewServerConn(netConn, config)
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
		go fileHandleTestSession(ch, requests, fs)
	}
}

func fileHandleTestSession(ch ssh.Channel, requests <-chan *ssh.Request, fs *fileTestFS) {
	defer ch.Close()
	for req := range requests {
		if req.Type == "exec" {
			cmdLen := uint32(req.Payload[0])<<24 | uint32(req.Payload[1])<<16 | uint32(req.Payload[2])<<8 | uint32(req.Payload[3])
			cmd := string(req.Payload[4 : 4+cmdLen])

			if req.WantReply {
				req.Reply(true, nil)
			}

			// Handle stdin-consuming commands
			if strings.HasPrefix(cmd, "cat > ") {
				path := fileExtractShellArg(cmd, "cat > ")
				stdinData, readErr := io.ReadAll(ch)
				exitCode := 0
				if readErr != nil {
					ch.Stderr().Write([]byte(fmt.Sprintf("read stdin: %v", readErr)))
					exitCode = 1
				} else {
					fs.mu.Lock()
					fs.files[path] = stdinData
					fs.mu.Unlock()
				}
				exitPayload := []byte{byte(exitCode >> 24), byte(exitCode >> 16), byte(exitCode >> 8), byte(exitCode)}
				ch.SendRequest("exit-status", false, exitPayload)
				return
			}

			stdout, exitCode := fs.handleExec(cmd)
			if exitCode != 0 {
				ch.Stderr().Write([]byte(stdout))
			} else {
				ch.Write([]byte(stdout))
			}

			exitPayload := []byte{byte(exitCode >> 24), byte(exitCode >> 16), byte(exitCode >> 8), byte(exitCode)}
			ch.SendRequest("exit-status", false, exitPayload)
			return
		}
		if req.WantReply {
			req.Reply(true, nil)
		}
	}
}

// setupFileTestSSH sets up test DB, SSH manager with a connected test client, and returns the instance + user + cleanup func.
func setupFileTestSSH(t *testing.T, fs *fileTestFS) (inst uint, user func() *http.Request, cleanup func()) {
	t.Helper()

	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr

	instance := createTestInstance(t, "bot-test", "Test")
	u := createTestUser(t, "admin")

	_, err = mgr.Connect(context.Background(), instance.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	return instance.ID, func() *http.Request { return nil }, func() {
		mgr.CloseAll()
		sshCleanup()
		_ = u // keep reference
	}
}

// --- BrowseFiles tests ---

func TestBrowseFiles_Success(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	_, err = mgr.Connect(context.Background(), inst.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root"
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["path"] != "/root" {
		t.Errorf("expected path '/root', got %v", result["path"])
	}
	entries, ok := result["entries"].([]interface{})
	if !ok {
		t.Fatalf("expected entries to be an array, got %T", result["entries"])
	}
	// Should have hello.txt and data.bin
	found := map[string]bool{}
	for _, e := range entries {
		entry := e.(map[string]interface{})
		found[entry["name"].(string)] = true
	}
	if !found["hello.txt"] {
		t.Error("expected hello.txt in directory listing")
	}
	if !found["data.bin"] {
		t.Error("expected data.bin in directory listing")
	}
}

func TestBrowseFiles_DefaultPath(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	// No path query parameter — should default to /root
	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["path"] != "/root" {
		t.Errorf("expected default path '/root', got %v", result["path"])
	}
}

func TestBrowseFiles_NonExistentDirectory(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse?path=/nonexistent", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/nonexistent"
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
}

func TestBrowseFiles_InstanceNotFound(t *testing.T) {
	setupTestDB(t)
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/999/files/browse", user, map[string]string{"id": "999"})
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestBrowseFiles_Forbidden(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned

	SSHMgr = sshproxy.NewSSHManager(nil, "")

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestBrowseFiles_NoSSHManager(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")
	SSHMgr = nil

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "SSH manager not initialized" {
		t.Errorf("expected 'SSH manager not initialized', got %v", result["detail"])
	}
}

func TestBrowseFiles_NoSSHConnection(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)
	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	// No connection established for this instance
	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "No SSH connection for instance" {
		t.Errorf("expected 'No SSH connection for instance', got %v", result["detail"])
	}
}

func TestBrowseFiles_InvalidID(t *testing.T) {
	setupTestDB(t)
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/notanumber/files/browse", user, map[string]string{"id": "notanumber"})
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

// --- ReadFileContent tests ---

func TestReadFileContent_Success(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/hello.txt", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root/hello.txt"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["path"] != "/root/hello.txt" {
		t.Errorf("expected path '/root/hello.txt', got %v", result["path"])
	}
	if result["content"] != "hello world" {
		t.Errorf("expected content 'hello world', got %v", result["content"])
	}
}

func TestReadFileContent_MissingPath(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "path parameter required" {
		t.Errorf("expected 'path parameter required', got %v", result["detail"])
	}
}

func TestReadFileContent_FileNotFound(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/nope.txt", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root/nope.txt"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
}

func TestReadFileContent_NoSSHConnection(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)
	SSHMgr = sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer SSHMgr.CloseAll()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/hello.txt", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root/hello.txt"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
}

// --- DownloadFile tests ---

func TestDownloadFile_Success(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/download?path=/root/hello.txt", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root/hello.txt"
	w := httptest.NewRecorder()

	DownloadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %s", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "hello.txt") {
		t.Errorf("expected Content-Disposition to contain 'hello.txt', got %s", cd)
	}
	if w.Body.String() != "hello world" {
		t.Errorf("expected body 'hello world', got %q", w.Body.String())
	}
}

func TestDownloadFile_MissingPath(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/download", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	DownloadFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestDownloadFile_NoSSHConnection(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)
	SSHMgr = sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer SSHMgr.CloseAll()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/download?path=/root/hello.txt", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root/hello.txt"
	w := httptest.NewRecorder()

	DownloadFile(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
}

// --- CreateNewFile tests ---

func TestCreateNewFile_Success(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	body, _ := json.Marshal(map[string]string{
		"path":    "/root/newfile.txt",
		"content": "new content",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateNewFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["success"] != true {
		t.Errorf("expected success true, got %v", result["success"])
	}
	if result["path"] != "/root/newfile.txt" {
		t.Errorf("expected path '/root/newfile.txt', got %v", result["path"])
	}

	// Verify file was written
	fs.mu.Lock()
	got, ok := fs.files["/root/newfile.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not created in test filesystem")
	}
	if string(got) != "new content" {
		t.Errorf("expected 'new content', got %q", string(got))
	}
}

func TestCreateNewFile_InvalidBody(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	SSHMgr = sshproxy.NewSSHManager(nil, "")

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.Body = io.NopCloser(strings.NewReader("not json"))
	w := httptest.NewRecorder()

	CreateNewFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestCreateNewFile_NoSSHConnection(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)
	SSHMgr = sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer SSHMgr.CloseAll()

	body, _ := json.Marshal(map[string]string{
		"path":    "/root/newfile.txt",
		"content": "content",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateNewFile(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
}

// --- CreateDirectory tests ---

func TestCreateDirectory_Success(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	body, _ := json.Marshal(map[string]string{
		"path": "/root/newdir",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/mkdir", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateDirectory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["success"] != true {
		t.Errorf("expected success true, got %v", result["success"])
	}

	fs.mu.Lock()
	_, ok := fs.dirs["/root/newdir"]
	fs.mu.Unlock()
	if !ok {
		t.Error("directory not created in test filesystem")
	}
}

func TestCreateDirectory_InvalidBody(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	SSHMgr = sshproxy.NewSSHManager(nil, "")

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/mkdir", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.Body = io.NopCloser(strings.NewReader("not json"))
	w := httptest.NewRecorder()

	CreateDirectory(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestCreateDirectory_NoSSHConnection(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)
	SSHMgr = sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer SSHMgr.CloseAll()

	body, _ := json.Marshal(map[string]string{
		"path": "/root/newdir",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/mkdir", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateDirectory(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
}

// --- UploadFile tests ---

func TestUploadFile_Success(t *testing.T) {
	fs := newFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)
	defer sshCleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr
	defer mgr.CloseAll()

	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	mgr.Connect(context.Background(), inst.ID, host, port)

	// Build multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "upload.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	part.Write([]byte("uploaded content"))
	writer.Close()

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root"
	req.Body = io.NopCloser(&buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["success"] != true {
		t.Errorf("expected success true, got %v", result["success"])
	}
	if result["filename"] != "upload.txt" {
		t.Errorf("expected filename 'upload.txt', got %v", result["filename"])
	}
	if result["path"] != "/root/upload.txt" {
		t.Errorf("expected path '/root/upload.txt', got %v", result["path"])
	}

	// Verify file was written
	fs.mu.Lock()
	got, ok := fs.files["/root/upload.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not created in test filesystem")
	}
	if string(got) != "uploaded content" {
		t.Errorf("expected 'uploaded content', got %q", string(got))
	}
}

func TestUploadFile_MissingPath(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestUploadFile_MissingFile(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root"
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}

	result := parseResponse(t, w)
	if result["detail"] != "file field required" {
		t.Errorf("expected 'file field required', got %v", result["detail"])
	}
}

func TestUploadFile_NoSSHConnection(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)
	SSHMgr = sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer SSHMgr.CloseAll()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "upload.txt")
	part.Write([]byte("content"))
	writer.Close()

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root"
	req.Body = io.NopCloser(&buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
}

func TestUploadFile_Forbidden(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "user") // non-admin, not assigned

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "upload.txt")
	part.Write([]byte("content"))
	writer.Close()

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	req.URL.RawQuery = "path=/root"
	req.Body = io.NopCloser(&buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

// --- Comprehensive SSH file operation handler tests ---

// Helper to set up a rich filesystem for comprehensive tests
func newRichFileTestFS() *fileTestFS {
	return &fileTestFS{
		files: map[string][]byte{
			"/root/hello.txt":         []byte("hello world"),
			"/root/data.bin":          {0x00, 0x01, 0x02, 0xFF, 0xFE, 0x80},
			"/root/config.json":       []byte(`{"key": "value", "items": [1, 2, 3]}`),
			"/root/multiline.txt":     []byte("line1\nline2\nline3\n"),
			"/root/unicode.txt":       []byte("café résumé 日本語"),
			"/root/subdir/nested.txt": []byte("nested content"),
			"/tmp/temp.log":           []byte("temporary data"),
			"/etc/hostname":           []byte("test-agent"),
		},
		dirs: map[string]bool{
			"/root":        true,
			"/root/subdir": true,
			"/tmp":         true,
			"/etc":         true,
		},
	}
}

// Helper to set up SSH test with rich filesystem and return reusable components
func setupRichFileTest(t *testing.T) (instID uint, user *database.User, fs *fileTestFS, cleanup func()) {
	t.Helper()

	fs = newRichFileTestFS()
	setupTestDB(t)

	pubKeyBytes, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, sshCleanup := fileTestSSHServer(t, signer.PublicKey(), fs)

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	mgr := sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	SSHMgr = mgr

	inst := createTestInstance(t, "bot-test", "Test")
	user = createTestUser(t, "admin")

	_, err = mgr.Connect(context.Background(), inst.ID, host, port)
	if err != nil {
		t.Fatalf("SSH connect: %v", err)
	}

	return inst.ID, user, fs, func() {
		mgr.CloseAll()
		sshCleanup()
	}
}

// --- BrowseFiles comprehensive tests ---

func TestBrowseFiles_TmpPath(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse?path=/tmp", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/tmp"
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["path"] != "/tmp" {
		t.Errorf("expected path '/tmp', got %v", result["path"])
	}
	entries := result["entries"].([]interface{})
	found := false
	for _, e := range entries {
		entry := e.(map[string]interface{})
		if entry["name"] == "temp.log" {
			found = true
		}
	}
	if !found {
		t.Error("temp.log not found in /tmp listing")
	}
}

func TestBrowseFiles_EtcPath(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse?path=/etc", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/etc"
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	entries := result["entries"].([]interface{})
	found := false
	for _, e := range entries {
		entry := e.(map[string]interface{})
		if entry["name"] == "hostname" {
			found = true
		}
	}
	if !found {
		t.Error("hostname not found in /etc listing")
	}
}

func TestBrowseFiles_WithSubdirectories(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root"
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	entries := result["entries"].([]interface{})

	foundFiles := map[string]bool{}
	foundDirs := map[string]bool{}
	for _, e := range entries {
		entry := e.(map[string]interface{})
		name := entry["name"].(string)
		typ := entry["type"].(string)
		if typ == "file" {
			foundFiles[name] = true
		} else if typ == "directory" {
			foundDirs[name] = true
		}
	}

	// Verify files
	for _, name := range []string{"hello.txt", "data.bin", "config.json", "multiline.txt", "unicode.txt"} {
		if !foundFiles[name] {
			t.Errorf("file %q not found in listing", name)
		}
	}
	// Verify subdirectory
	if !foundDirs["subdir"] {
		t.Error("subdirectory 'subdir' not found in listing")
	}
}

func TestBrowseFiles_NestedSubdirectory(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse?path=/root/subdir", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/subdir"
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	entries := result["entries"].([]interface{})
	found := false
	for _, e := range entries {
		entry := e.(map[string]interface{})
		if entry["name"] == "nested.txt" {
			found = true
		}
	}
	if !found {
		t.Error("nested.txt not found in subdirectory listing")
	}
}

// --- ReadFileContent comprehensive tests ---

func TestReadFileContent_BinaryFile(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/data.bin", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/data.bin"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["path"] != "/root/data.bin" {
		t.Errorf("expected path '/root/data.bin', got %v", result["path"])
	}
	// Binary content will be in the response as a string
	content, ok := result["content"].(string)
	if !ok {
		t.Fatal("expected content field in response")
	}
	if len(content) == 0 {
		t.Error("expected non-empty content for binary file")
	}
}

func TestReadFileContent_JSONFile(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/config.json", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/config.json"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	content := result["content"].(string)
	if !strings.Contains(content, `"key": "value"`) {
		t.Errorf("expected JSON content, got %q", content)
	}
}

func TestReadFileContent_MultilineFile(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/multiline.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/multiline.txt"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	content := result["content"].(string)
	if !strings.Contains(content, "line1") || !strings.Contains(content, "line3") {
		t.Errorf("expected multiline content, got %q", content)
	}
}

func TestReadFileContent_UnicodeFile(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/unicode.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/unicode.txt"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	content := result["content"].(string)
	if !strings.Contains(content, "café") || !strings.Contains(content, "日本語") {
		t.Errorf("expected unicode content, got %q", content)
	}
}

func TestReadFileContent_NestedPath(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/subdir/nested.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/subdir/nested.txt"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["content"] != "nested content" {
		t.Errorf("expected 'nested content', got %v", result["content"])
	}
}

func TestReadFileContent_EtcFile(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/etc/hostname", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/etc/hostname"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["content"] != "test-agent" {
		t.Errorf("expected 'test-agent', got %v", result["content"])
	}
}

// --- DownloadFile comprehensive tests ---

func TestDownloadFile_BinaryContent(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/download?path=/root/data.bin", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/data.bin"
	w := httptest.NewRecorder()

	DownloadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %s", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "data.bin") {
		t.Errorf("expected Content-Disposition to contain 'data.bin', got %s", cd)
	}

	body := w.Body.Bytes()
	expected := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x80}
	if len(body) != len(expected) {
		t.Fatalf("expected %d bytes, got %d", len(expected), len(body))
	}
	for i := range expected {
		if body[i] != expected[i] {
			t.Fatalf("byte mismatch at offset %d: expected 0x%02x, got 0x%02x", i, expected[i], body[i])
		}
	}
}

func TestDownloadFile_JSONContent(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/download?path=/root/config.json", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/config.json"
	w := httptest.NewRecorder()

	DownloadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "config.json") {
		t.Errorf("expected Content-Disposition to contain 'config.json', got %s", cd)
	}
	if !strings.Contains(w.Body.String(), `"key": "value"`) {
		t.Error("expected JSON content in download body")
	}
}

func TestDownloadFile_NestedPath(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/download?path=/root/subdir/nested.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/subdir/nested.txt"
	w := httptest.NewRecorder()

	DownloadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "nested.txt") {
		t.Errorf("expected filename 'nested.txt' in Content-Disposition, got %s", cd)
	}
	if w.Body.String() != "nested content" {
		t.Errorf("expected 'nested content', got %q", w.Body.String())
	}
}

func TestDownloadFile_FileNotFound(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/download?path=/root/nonexistent.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/nonexistent.txt"
	w := httptest.NewRecorder()

	DownloadFile(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
}

// --- CreateNewFile comprehensive tests ---

func TestCreateNewFile_JSONContent(t *testing.T) {
	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"path":    "/root/new_config.json",
		"content": `{"database": "sqlite", "port": 8080}`,
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateNewFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	fs.mu.Lock()
	got := fs.files["/root/new_config.json"]
	fs.mu.Unlock()
	if !strings.Contains(string(got), `"database": "sqlite"`) {
		t.Errorf("expected JSON content, got %q", string(got))
	}
}

func TestCreateNewFile_MultilineScript(t *testing.T) {
	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	scriptContent := "#!/bin/bash\necho 'hello'\nfor i in 1 2 3; do\n  echo $i\ndone\n"
	body, _ := json.Marshal(map[string]string{
		"path":    "/root/script.sh",
		"content": scriptContent,
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateNewFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	fs.mu.Lock()
	got := fs.files["/root/script.sh"]
	fs.mu.Unlock()
	if string(got) != scriptContent {
		t.Errorf("expected script content, got %q", string(got))
	}
}

func TestCreateNewFile_EmptyContent(t *testing.T) {
	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"path":    "/root/empty.txt",
		"content": "",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateNewFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/empty.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("empty file not created")
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

func TestCreateNewFile_UnicodeContent(t *testing.T) {
	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"path":    "/root/i18n.txt",
		"content": "café résumé 日本語 中文",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateNewFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	fs.mu.Lock()
	got := fs.files["/root/i18n.txt"]
	fs.mu.Unlock()
	if !strings.Contains(string(got), "café") || !strings.Contains(string(got), "日本語") {
		t.Errorf("expected unicode content, got %q", string(got))
	}
}

// --- CreateDirectory comprehensive tests ---

func TestCreateDirectory_NestedPaths(t *testing.T) {
	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"path": "/root/a/b/c/d",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/mkdir", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateDirectory(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["path"] != "/root/a/b/c/d" {
		t.Errorf("expected path '/root/a/b/c/d', got %v", result["path"])
	}

	fs.mu.Lock()
	for _, p := range []string{"/root/a", "/root/a/b", "/root/a/b/c", "/root/a/b/c/d"} {
		if !fs.dirs[p] {
			t.Errorf("expected directory %s to exist", p)
		}
	}
	fs.mu.Unlock()
}

func TestCreateDirectory_AlreadyExists(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{
		"path": "/root",
	})

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/mkdir", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	CreateDirectory(w, req)

	// mkdir -p should succeed even for existing directories
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

// --- UploadFile comprehensive tests ---

func TestUploadFile_BinaryContent(t *testing.T) {
	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	binaryData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header bytes

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	part.Write(binaryData)
	writer.Close()

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root"
	req.Body = io.NopCloser(&buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["filename"] != "image.png" {
		t.Errorf("expected filename 'image.png', got %v", result["filename"])
	}
	if result["path"] != "/root/image.png" {
		t.Errorf("expected path '/root/image.png', got %v", result["path"])
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/image.png"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("binary file not created")
	}
	if len(got) != len(binaryData) {
		t.Fatalf("expected %d bytes, got %d", len(binaryData), len(got))
	}
	for i := range binaryData {
		if got[i] != binaryData[i] {
			t.Fatalf("byte mismatch at offset %d: expected 0x%02x, got 0x%02x", i, binaryData[i], got[i])
		}
	}
}

func TestUploadFile_LargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file upload test in short mode")
	}

	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	// Create a file >10MB
	size := 10*1024*1024 + 100
	largeData := make([]byte, size)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "large.bin")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	part.Write(largeData)
	writer.Close()

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload?path=/root", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root"
	req.Body = io.NopCloser(&buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/large.bin"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("large file not created")
	}
	if len(got) != size {
		t.Fatalf("expected %d bytes, got %d", size, len(got))
	}

	// Verify integrity at key offsets
	for _, offset := range []int{0, 1024, size / 2, size - 1} {
		if got[offset] != largeData[offset] {
			t.Fatalf("byte mismatch at offset %d: expected 0x%02x, got 0x%02x", offset, largeData[offset], got[offset])
		}
	}
}

func TestUploadFile_ToSubdirectory(t *testing.T) {
	instID, user, fs, cleanup := setupRichFileTest(t)
	defer cleanup()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "readme.md")
	part.Write([]byte("# README\n\nSome documentation"))
	writer.Close()

	req := buildRequest(t, "POST", "/api/v1/instances/1/files/upload?path=/root/subdir", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/subdir"
	req.Body = io.NopCloser(&buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	UploadFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	result := parseResponse(t, w)
	if result["path"] != "/root/subdir/readme.md" {
		t.Errorf("expected path '/root/subdir/readme.md', got %v", result["path"])
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/subdir/readme.md"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not uploaded to subdirectory")
	}
	if !strings.Contains(string(got), "# README") {
		t.Errorf("expected markdown content, got %q", string(got))
	}
}

// --- Error message tests ---

func TestBrowseFiles_NotFoundErrorMessage(t *testing.T) {
	setupTestDB(t)
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", "/api/v1/instances/999/files/browse", user, map[string]string{"id": "999"})
	w := httptest.NewRecorder()

	BrowseFiles(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
	result := parseResponse(t, w)
	detail, ok := result["detail"].(string)
	if !ok || !strings.Contains(detail, "not found") {
		t.Errorf("expected 'not found' error message, got %v", result["detail"])
	}
}

func TestReadFileContent_NotFoundErrorMessage(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	req := buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/nonexistent.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/nonexistent.txt"
	w := httptest.NewRecorder()

	ReadFileContent(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
	result := parseResponse(t, w)
	detail, ok := result["detail"].(string)
	if !ok {
		t.Fatal("expected detail in error response")
	}
	if !strings.Contains(strings.ToLower(detail), "fail") && !strings.Contains(strings.ToLower(detail), "not found") && !strings.Contains(strings.ToLower(detail), "no such file") {
		t.Errorf("expected descriptive error message, got %q", detail)
	}
}

func TestBrowseFiles_SSHManagerErrorMessages(t *testing.T) {
	setupTestDB(t)
	inst := createTestInstance(t, "bot-test", "Test")
	user := createTestUser(t, "admin")

	// Test nil SSH manager message
	SSHMgr = nil
	req := buildRequest(t, "GET", "/api/v1/instances/1/files/browse", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w := httptest.NewRecorder()
	BrowseFiles(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
	result := parseResponse(t, w)
	if result["detail"] != "SSH manager not initialized" {
		t.Errorf("expected 'SSH manager not initialized', got %v", result["detail"])
	}

	// Test no SSH connection message
	pubKeyBytes, privKeyPEM, _ := sshproxy.GenerateKeyPair()
	signer, _ := sshproxy.ParsePrivateKey(privKeyPEM)
	SSHMgr = sshproxy.NewSSHManager(signer, string(pubKeyBytes))
	defer SSHMgr.CloseAll()

	req2 := buildRequest(t, "GET", "/api/v1/instances/1/files/browse", user, map[string]string{"id": fmt.Sprintf("%d", inst.ID)})
	w2 := httptest.NewRecorder()
	BrowseFiles(w2, req2)

	if w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w2.Code)
	}
	result2 := parseResponse(t, w2)
	if result2["detail"] != "No SSH connection for instance" {
		t.Errorf("expected 'No SSH connection for instance', got %v", result2["detail"])
	}
}

// --- End-to-end create-then-browse-then-download workflow test ---

func TestFileOperations_EndToEndWorkflow(t *testing.T) {
	instID, user, _, cleanup := setupRichFileTest(t)
	defer cleanup()

	// 1. Create a directory
	body, _ := json.Marshal(map[string]string{"path": "/root/workflow"})
	req := buildRequest(t, "POST", "/api/v1/instances/1/files/mkdir", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	CreateDirectory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mkdir failed: %d: %s", w.Code, w.Body.String())
	}

	// 2. Create a file in the directory
	body, _ = json.Marshal(map[string]string{
		"path":    "/root/workflow/data.txt",
		"content": "workflow test data",
	})
	req = buildRequest(t, "POST", "/api/v1/instances/1/files/create", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	CreateNewFile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create file failed: %d: %s", w.Code, w.Body.String())
	}

	// 3. Browse the directory to verify
	req = buildRequest(t, "GET", "/api/v1/instances/1/files/browse?path=/root/workflow", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/workflow"
	w = httptest.NewRecorder()
	BrowseFiles(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("browse failed: %d: %s", w.Code, w.Body.String())
	}
	result := parseResponse(t, w)
	entries := result["entries"].([]interface{})
	found := false
	for _, e := range entries {
		entry := e.(map[string]interface{})
		if entry["name"] == "data.txt" {
			found = true
		}
	}
	if !found {
		t.Error("data.txt not found in workflow directory listing")
	}

	// 4. Read the file content
	req = buildRequest(t, "GET", "/api/v1/instances/1/files/read?path=/root/workflow/data.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/workflow/data.txt"
	w = httptest.NewRecorder()
	ReadFileContent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("read file failed: %d: %s", w.Code, w.Body.String())
	}
	result = parseResponse(t, w)
	if result["content"] != "workflow test data" {
		t.Errorf("expected 'workflow test data', got %v", result["content"])
	}

	// 5. Download the file
	req = buildRequest(t, "GET", "/api/v1/instances/1/files/download?path=/root/workflow/data.txt", user, map[string]string{"id": fmt.Sprintf("%d", instID)})
	req.URL.RawQuery = "path=/root/workflow/data.txt"
	w = httptest.NewRecorder()
	DownloadFile(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("download failed: %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("expected Content-Type application/octet-stream, got %s", ct)
	}
	if w.Body.String() != "workflow test data" {
		t.Errorf("expected 'workflow test data', got %q", w.Body.String())
	}
}
