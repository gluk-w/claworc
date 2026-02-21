package sshfiles

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// testFS simulates a simple in-memory filesystem for the test SSH server.
type testFS struct {
	mu    sync.Mutex
	files map[string][]byte // path → content
	dirs  map[string]bool   // path → exists
}

func newTestFS() *testFS {
	return &testFS{
		files: map[string][]byte{
			"/root/hello.txt": []byte("hello world"),
			"/root/binary":    {0x00, 0x01, 0x02, 0xFF},
		},
		dirs: map[string]bool{
			"/root": true,
			"/tmp":  true,
		},
	}
}

// handleExec processes an exec command against the in-memory filesystem.
func (fs *testFS) handleExec(cmd string) (stdout string, exitCode int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Parse the command
	switch {
	case strings.HasPrefix(cmd, "ls -la --color=never "):
		path := extractShellArg(cmd, "ls -la --color=never ")
		return fs.handleLs(path)

	case strings.HasPrefix(cmd, "cat "):
		path := extractShellArg(cmd, "cat ")
		return fs.handleCat(path)

	case strings.HasPrefix(cmd, "mkdir -p "):
		path := extractShellArg(cmd, "mkdir -p ")
		return fs.handleMkdir(path)

	case strings.HasPrefix(cmd, "> "):
		// Truncate file: > '/path'
		path := extractShellArg(cmd, "> ")
		fs.files[path] = []byte{}
		return "", 0

	case strings.HasPrefix(cmd, "echo '") && strings.Contains(cmd, "| base64 -d >>"):
		return fs.handleBase64Append(cmd)

	default:
		return fmt.Sprintf("unknown command: %s", cmd), 127
	}
}

func (fs *testFS) handleLs(path string) (string, int) {
	if !fs.dirs[path] {
		return "", 2 // ls: cannot access: No such file or directory → stderr, but we return via exit code
	}

	var lines []string
	lines = append(lines, "total 8")

	// List files that are directly in this directory
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
	// List subdirectories
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

func (fs *testFS) handleCat(path string) (string, int) {
	content, ok := fs.files[path]
	if !ok {
		return "", 1 // cat: file not found → stderr
	}
	return string(content), 0
}

func (fs *testFS) handleMkdir(path string) (string, int) {
	// mkdir -p: create all parent directories
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

func (fs *testFS) handleBase64Append(cmd string) (string, int) {
	// echo '<b64>' | base64 -d >> '/path'
	start := strings.Index(cmd, "'") + 1
	end := strings.Index(cmd[start:], "'") + start
	b64 := cmd[start:end]

	pathStart := strings.LastIndex(cmd, ">> ") + 3
	path := extractQuotedArg(cmd[pathStart:])

	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Sprintf("base64 decode error: %v", err), 1
	}

	fs.files[path] = append(fs.files[path], decoded...)
	return "", 0
}

// extractShellArg extracts a shell-quoted argument from a command after removing the prefix.
func extractShellArg(cmd, prefix string) string {
	rest := strings.TrimPrefix(cmd, prefix)
	return extractQuotedArg(rest)
}

// extractQuotedArg extracts a single-quoted argument, handling escaped quotes.
func extractQuotedArg(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "'") {
		// Unquoted — return first word
		return strings.Fields(s)[0]
	}

	// Handle single-quoted strings with possible escaped quotes ('\'')
	var result strings.Builder
	i := 1 // skip opening quote
	for i < len(s) {
		if s[i] == '\'' {
			// Check for escaped quote pattern: '\''
			if i+3 < len(s) && s[i:i+4] == "'\\''" {
				result.WriteByte('\'')
				i += 4
				continue
			}
			break // closing quote
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// testSSHServer starts an in-process SSH server backed by the given testFS.
// It handles exec requests by dispatching to the filesystem.
func testSSHServer(t *testing.T, authorizedKey ssh.PublicKey, fs *testFS) (addr string, cleanup func()) {
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
			go handleTestConn(netConn, config, fs)
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

func handleTestConn(netConn net.Conn, config *ssh.ServerConfig, fs *testFS) {
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
		go handleTestSession(ch, requests, fs)
	}
}

func handleTestSession(ch ssh.Channel, requests <-chan *ssh.Request, fs *testFS) {
	defer ch.Close()
	for req := range requests {
		if req.Type == "exec" {
			// Payload format: uint32 length + command string
			cmdLen := uint32(req.Payload[0])<<24 | uint32(req.Payload[1])<<16 | uint32(req.Payload[2])<<8 | uint32(req.Payload[3])
			cmd := string(req.Payload[4 : 4+cmdLen])

			if req.WantReply {
				req.Reply(true, nil)
			}

			// Check if this is a stdin-consuming command (e.g. cat > '/path')
			if strings.HasPrefix(cmd, "cat > ") {
				path := extractShellArg(cmd, "cat > ")
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
				// Write error to stderr
				ch.Stderr().Write([]byte(stdout))
			} else {
				ch.Write([]byte(stdout))
			}

			// Send exit-status
			exitPayload := []byte{byte(exitCode >> 24), byte(exitCode >> 16), byte(exitCode >> 8), byte(exitCode)}
			ch.SendRequest("exit-status", false, exitPayload)

			return
		}
		if req.WantReply {
			req.Reply(true, nil)
		}
	}
}

// newTestClient creates an SSH client connected to a test server with the given filesystem.
func newTestClient(t *testing.T, fs *testFS) (*ssh.Client, func()) {
	t.Helper()

	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServer(t, signer.PublicKey(), fs)

	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), cfg)
	if err != nil {
		cleanup()
		t.Fatalf("dial test server: %v", err)
	}

	return client, func() {
		client.Close()
		cleanup()
	}
}

// --- executeCommand tests ---

func TestExecuteCommand_Success(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	stdout, stderr, exitCode, err := executeCommand(client, "ls -la --color=never '/root'")
	if err != nil {
		t.Fatalf("executeCommand error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d (stderr: %s)", exitCode, stderr)
	}
	if !strings.Contains(stdout, "hello.txt") {
		t.Errorf("expected stdout to contain hello.txt, got: %s", stdout)
	}
}

func TestExecuteCommand_NonZeroExit(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	_, _, exitCode, err := executeCommand(client, "cat '/nonexistent'")
	if err != nil {
		t.Fatalf("executeCommand error: %v", err)
	}
	if exitCode == 0 {
		t.Error("expected non-zero exit code for nonexistent file")
	}
}

func TestExecuteCommand_UnknownCommand(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	_, _, exitCode, err := executeCommand(client, "notacommand")
	if err != nil {
		t.Fatalf("executeCommand error: %v", err)
	}
	if exitCode != 127 {
		t.Errorf("expected exit code 127, got %d", exitCode)
	}
}

// --- ListDirectory tests ---

func TestListDirectory_Root(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	entries, err := ListDirectory(client, "/root")
	if err != nil {
		t.Fatalf("ListDirectory error: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "hello.txt" {
			found = true
			if e.Type != "file" {
				t.Errorf("expected type 'file', got %q", e.Type)
			}
			if e.Permissions != "-rw-r--r--" {
				t.Errorf("expected permissions -rw-r--r--, got %q", e.Permissions)
			}
			if e.Size == nil {
				t.Error("expected non-nil size for file")
			} else if *e.Size != "11" {
				t.Errorf("expected size '11', got %q", *e.Size)
			}
		}
	}
	if !found {
		t.Error("hello.txt not found in listing")
	}
}

func TestListDirectory_NonExistent(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	_, err := ListDirectory(client, "/nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

func TestListDirectory_Empty(t *testing.T) {
	fs := newTestFS()
	// /tmp exists but has no files
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	entries, err := ListDirectory(client, "/tmp")
	if err != nil {
		t.Fatalf("ListDirectory error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// --- ReadFile tests ---

func TestReadFile_TextFile(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	data, err := ReadFile(client, "/root/hello.txt")
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

func TestReadFile_NonExistent(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	_, err := ReadFile(client, "/root/nope.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

// --- WriteFile tests ---

func TestWriteFile_SmallFile(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	content := []byte("new file content")
	err := WriteFile(client, "/root/newfile.txt", content)
	if err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	// Verify the file was written to the test filesystem
	fs.mu.Lock()
	got, ok := fs.files["/root/newfile.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not created in test filesystem")
	}
	if string(got) != string(content) {
		t.Errorf("expected %q, got %q", string(content), string(got))
	}
}

func TestWriteFile_LargeFile(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	// Create a file larger than 48KB chunk size to test chunking
	content := make([]byte, 100000)
	for i := range content {
		content[i] = byte(i % 256)
	}

	err := WriteFile(client, "/root/large.bin", content)
	if err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/large.bin"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not created in test filesystem")
	}
	if len(got) != len(content) {
		t.Fatalf("expected %d bytes, got %d", len(content), len(got))
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("byte mismatch at offset %d: expected %d, got %d", i, content[i], got[i])
		}
	}
}

func TestWriteFile_EmptyFile(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	err := WriteFile(client, "/root/empty.txt", []byte{})
	if err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/empty.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not created in test filesystem")
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

// --- CreateDirectory tests ---

func TestCreateDirectory_Simple(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	err := CreateDirectory(client, "/root/newdir")
	if err != nil {
		t.Fatalf("CreateDirectory error: %v", err)
	}

	fs.mu.Lock()
	_, ok := fs.dirs["/root/newdir"]
	fs.mu.Unlock()
	if !ok {
		t.Error("directory not created in test filesystem")
	}
}

func TestCreateDirectory_Nested(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	err := CreateDirectory(client, "/root/a/b/c")
	if err != nil {
		t.Fatalf("CreateDirectory error: %v", err)
	}

	fs.mu.Lock()
	for _, p := range []string{"/root/a", "/root/a/b", "/root/a/b/c"} {
		if !fs.dirs[p] {
			t.Errorf("expected directory %s to exist", p)
		}
	}
	fs.mu.Unlock()
}

func TestCreateDirectory_AlreadyExists(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	// /root already exists, mkdir -p should succeed
	err := CreateDirectory(client, "/root")
	if err != nil {
		t.Fatalf("CreateDirectory error: %v", err)
	}
}

// --- shellQuote tests ---

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "'simple'"},
		{"/root/file.txt", "'/root/file.txt'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- executeCommandWithStdin tests ---

func TestExecuteCommandWithStdin_Success(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	content := []byte("hello from stdin")
	err := executeCommandWithStdin(client, "cat > '/tmp/stdin_test.txt'", content)
	if err != nil {
		t.Fatalf("executeCommandWithStdin error: %v", err)
	}

	fs.mu.Lock()
	got, ok := fs.files["/tmp/stdin_test.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not created via stdin piping")
	}
	if string(got) != string(content) {
		t.Errorf("expected %q, got %q", string(content), string(got))
	}
}

func TestExecuteCommandWithStdin_BinaryData(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	// Binary data with null bytes and high bytes
	content := []byte{0x00, 0x01, 0x02, 0xFE, 0xFF, 0x00, 0x80}
	err := executeCommandWithStdin(client, "cat > '/tmp/binary.bin'", content)
	if err != nil {
		t.Fatalf("executeCommandWithStdin error: %v", err)
	}

	fs.mu.Lock()
	got, ok := fs.files["/tmp/binary.bin"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("binary file not created via stdin piping")
	}
	if len(got) != len(content) {
		t.Fatalf("expected %d bytes, got %d", len(content), len(got))
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("byte mismatch at offset %d: expected 0x%02x, got 0x%02x", i, content[i], got[i])
		}
	}
}

func TestExecuteCommandWithStdin_EmptyInput(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	err := executeCommandWithStdin(client, "cat > '/tmp/empty_stdin.txt'", []byte{})
	if err != nil {
		t.Fatalf("executeCommandWithStdin error: %v", err)
	}

	fs.mu.Lock()
	got, ok := fs.files["/tmp/empty_stdin.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("empty file not created via stdin piping")
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

func TestExecuteCommandWithStdin_ClosedClient(t *testing.T) {
	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	fs := newTestFS()
	addr, cleanup := testSSHServer(t, signer.PublicKey(), fs)
	defer cleanup()

	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Close the client and then try executeCommandWithStdin - should fail
	client.Close()

	err = executeCommandWithStdin(client, "cat > /tmp/test", []byte("test"))
	if err == nil {
		t.Error("expected error with closed client")
	}
}
