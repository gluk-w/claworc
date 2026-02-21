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
