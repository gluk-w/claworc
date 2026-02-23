package sshproxy

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
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

// newRichTestFS returns a testFS pre-populated with files and directories at various paths
// to support comprehensive tests for directory listing, binary reads, etc.
func newRichTestFS() *testFS {
	return &testFS{
		files: map[string][]byte{
			"/root/hello.txt":           []byte("hello world"),
			"/root/binary":              {0x00, 0x01, 0x02, 0xFF},
			"/root/config.json":         []byte(`{"key": "value", "nested": {"a": 1}}`),
			"/root/multiline.txt":       []byte("line1\nline2\nline3\n"),
			"/root/unicode.txt":         []byte("café résumé naïve ñ 日本語"),
			"/tmp/scratch.log":          []byte("temporary data"),
			"/etc/hostname":             []byte("test-agent"),
			"/etc/os-release":           []byte("PRETTY_NAME=\"Ubuntu 24.04\"\nID=ubuntu\n"),
			"/root/subdir/nested.txt":   []byte("nested file"),
			"/root/special chars/a b.txt": []byte("file with spaces"),
		},
		dirs: map[string]bool{
			"/root":               true,
			"/tmp":                true,
			"/etc":                true,
			"/root/subdir":        true,
			"/root/special chars": true,
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
func testSSHServerWithFS(t *testing.T, authorizedKey ssh.PublicKey, fs *testFS) (addr string, cleanup func()) {
	t.Helper()

	_, hostKeyPEM, err := GenerateKeyPair()
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

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	addr, cleanup := testSSHServerWithFS(t, signer.PublicKey(), fs)

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
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	fs := newTestFS()
	addr, cleanup := testSSHServerWithFS(t, signer.PublicKey(), fs)
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

// --- Comprehensive tests for SSH-based file operations ---

// ListDirectory at various paths

func TestListDirectory_TmpPath(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	entries, err := ListDirectory(client, "/tmp")
	if err != nil {
		t.Fatalf("ListDirectory /tmp error: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "scratch.log" {
			found = true
			if e.Type != "file" {
				t.Errorf("expected type 'file', got %q", e.Type)
			}
		}
	}
	if !found {
		t.Error("scratch.log not found in /tmp listing")
	}
}

func TestListDirectory_EtcPath(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	entries, err := ListDirectory(client, "/etc")
	if err != nil {
		t.Fatalf("ListDirectory /etc error: %v", err)
	}

	foundNames := map[string]bool{}
	for _, e := range entries {
		foundNames[e.Name] = true
	}
	if !foundNames["hostname"] {
		t.Error("hostname not found in /etc listing")
	}
	if !foundNames["os-release"] {
		t.Error("os-release not found in /etc listing")
	}
}

func TestListDirectory_WithFilesAndSubdirectories(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	entries, err := ListDirectory(client, "/root")
	if err != nil {
		t.Fatalf("ListDirectory /root error: %v", err)
	}

	foundFiles := map[string]bool{}
	foundDirs := map[string]bool{}
	for _, e := range entries {
		if e.Type == "file" {
			foundFiles[e.Name] = true
		} else if e.Type == "directory" {
			foundDirs[e.Name] = true
		}
	}

	// Files
	for _, name := range []string{"hello.txt", "binary", "config.json", "multiline.txt", "unicode.txt"} {
		if !foundFiles[name] {
			t.Errorf("file %q not found in /root listing", name)
		}
	}
	// Subdirectories
	if !foundDirs["subdir"] {
		t.Error("subdirectory 'subdir' not found in /root listing")
	}
	if !foundDirs["special chars"] {
		t.Error("subdirectory 'special chars' not found in /root listing")
	}
}

func TestListDirectory_NestedSubdirectory(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	entries, err := ListDirectory(client, "/root/subdir")
	if err != nil {
		t.Fatalf("ListDirectory /root/subdir error: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "nested.txt" {
			found = true
		}
	}
	if !found {
		t.Error("nested.txt not found in /root/subdir listing")
	}
}

// ReadFile — binary files

func TestReadFile_BinaryFile(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	data, err := ReadFile(client, "/root/binary")
	if err != nil {
		t.Fatalf("ReadFile binary error: %v", err)
	}

	expected := []byte{0x00, 0x01, 0x02, 0xFF}
	if len(data) != len(expected) {
		t.Fatalf("expected %d bytes, got %d", len(expected), len(data))
	}
	for i := range expected {
		if data[i] != expected[i] {
			t.Fatalf("byte mismatch at offset %d: expected 0x%02x, got 0x%02x", i, expected[i], data[i])
		}
	}
}

func TestReadFile_JSONContent(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	data, err := ReadFile(client, "/root/config.json")
	if err != nil {
		t.Fatalf("ReadFile JSON error: %v", err)
	}
	if !strings.Contains(string(data), `"key": "value"`) {
		t.Errorf("expected JSON content, got %q", string(data))
	}
}

func TestReadFile_MultilineContent(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	data, err := ReadFile(client, "/root/multiline.txt")
	if err != nil {
		t.Fatalf("ReadFile multiline error: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines, got %d", len(lines))
	}
}

func TestReadFile_UnicodeContent(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	data, err := ReadFile(client, "/root/unicode.txt")
	if err != nil {
		t.Fatalf("ReadFile unicode error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "café") {
		t.Errorf("expected unicode content with 'café', got %q", content)
	}
	if !strings.Contains(content, "日本語") {
		t.Errorf("expected unicode content with Japanese chars, got %q", content)
	}
}

func TestReadFile_AtVariousPaths(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	tests := []struct {
		path     string
		expected string
	}{
		{"/etc/hostname", "test-agent"},
		{"/tmp/scratch.log", "temporary data"},
		{"/root/subdir/nested.txt", "nested file"},
	}
	for _, tt := range tests {
		data, err := ReadFile(client, tt.path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error: %v", tt.path, err)
		}
		if string(data) != tt.expected {
			t.Errorf("ReadFile(%s) = %q, want %q", tt.path, string(data), tt.expected)
		}
	}
}

// WriteFile — various content types

func TestWriteFile_JSONContent(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	content := []byte(`{"name": "test", "values": [1, 2, 3], "nested": {"key": "value"}}`)
	err := WriteFile(client, "/root/config.json", content)
	if err != nil {
		t.Fatalf("WriteFile JSON error: %v", err)
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/config.json"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("JSON file not created")
	}
	if string(got) != string(content) {
		t.Errorf("JSON content mismatch: got %q", string(got))
	}
}

func TestWriteFile_MultilineContent(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	content := []byte("#!/bin/bash\necho 'hello'\necho 'world'\nexit 0\n")
	err := WriteFile(client, "/root/script.sh", content)
	if err != nil {
		t.Fatalf("WriteFile multiline error: %v", err)
	}

	fs.mu.Lock()
	got := fs.files["/root/script.sh"]
	fs.mu.Unlock()
	if string(got) != string(content) {
		t.Errorf("multiline content mismatch: got %q", string(got))
	}
}

func TestWriteFile_SpecialCharacters(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	content := []byte("line with 'quotes' and \"double quotes\"\ttabs\nand $variables ${VAR}")
	err := WriteFile(client, "/root/special.txt", content)
	if err != nil {
		t.Fatalf("WriteFile special chars error: %v", err)
	}

	fs.mu.Lock()
	got := fs.files["/root/special.txt"]
	fs.mu.Unlock()
	if string(got) != string(content) {
		t.Errorf("special chars content mismatch: got %q", string(got))
	}
}

func TestWriteFile_UnicodeContent(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	content := []byte("café résumé naïve ñ 日本語 中文 한국어 🚀")
	err := WriteFile(client, "/root/unicode.txt", content)
	if err != nil {
		t.Fatalf("WriteFile unicode error: %v", err)
	}

	fs.mu.Lock()
	got := fs.files["/root/unicode.txt"]
	fs.mu.Unlock()
	if string(got) != string(content) {
		t.Errorf("unicode content mismatch: got %q, want %q", string(got), string(content))
	}
}

func TestWriteFile_BinaryContent(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	// Generate binary content with every possible byte value
	content := make([]byte, 256)
	for i := range content {
		content[i] = byte(i)
	}
	err := WriteFile(client, "/root/allbytes.bin", content)
	if err != nil {
		t.Fatalf("WriteFile binary error: %v", err)
	}

	fs.mu.Lock()
	got := fs.files["/root/allbytes.bin"]
	fs.mu.Unlock()
	if len(got) != 256 {
		t.Fatalf("expected 256 bytes, got %d", len(got))
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("byte mismatch at offset %d: expected 0x%02x, got 0x%02x", i, content[i], got[i])
		}
	}
}

func TestWriteFile_VeryLargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	// Create a 10MB+ file to test chunked streaming
	size := 10*1024*1024 + 42 // 10MB + 42 bytes to ensure non-aligned chunks
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 256)
	}

	err := WriteFile(client, "/root/large.bin", content)
	if err != nil {
		t.Fatalf("WriteFile large file error: %v", err)
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

	// Verify content integrity by sampling
	for _, offset := range []int{0, 1024, size / 2, size - 1} {
		if got[offset] != content[offset] {
			t.Fatalf("byte mismatch at offset %d: expected 0x%02x, got 0x%02x", offset, content[offset], got[offset])
		}
	}
}

// CreateDirectory — deeply nested paths

func TestCreateDirectory_DeeplyNested(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	err := CreateDirectory(client, "/root/a/b/c/d/e/f")
	if err != nil {
		t.Fatalf("CreateDirectory deeply nested error: %v", err)
	}

	fs.mu.Lock()
	for _, p := range []string{"/root/a", "/root/a/b", "/root/a/b/c", "/root/a/b/c/d", "/root/a/b/c/d/e", "/root/a/b/c/d/e/f"} {
		if !fs.dirs[p] {
			t.Errorf("expected directory %s to exist", p)
		}
	}
	fs.mu.Unlock()
}

func TestCreateDirectory_ThenWriteFile(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	// Create directory, then write a file into it
	err := CreateDirectory(client, "/root/newdir/sub")
	if err != nil {
		t.Fatalf("CreateDirectory error: %v", err)
	}

	content := []byte("file in new dir")
	err = WriteFile(client, "/root/newdir/sub/file.txt", content)
	if err != nil {
		t.Fatalf("WriteFile in new dir error: %v", err)
	}

	fs.mu.Lock()
	got, ok := fs.files["/root/newdir/sub/file.txt"]
	fs.mu.Unlock()
	if !ok {
		t.Fatal("file not created in new directory")
	}
	if string(got) != "file in new dir" {
		t.Errorf("expected 'file in new dir', got %q", string(got))
	}
}

// Error cases

func TestReadFile_EmptyPath(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	_, err := ReadFile(client, "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestListDirectory_ClosedClient(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	fs := newTestFS()
	addr, cleanup := testSSHServerWithFS(t, signer.PublicKey(), fs)
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
	client.Close()

	_, err = ListDirectory(client, "/root")
	if err == nil {
		t.Error("expected error with closed client")
	}
}

func TestWriteFile_ClosedClient(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	fs := newTestFS()
	addr, cleanup := testSSHServerWithFS(t, signer.PublicKey(), fs)
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
	client.Close()

	err = WriteFile(client, "/root/test.txt", []byte("data"))
	if err == nil {
		t.Error("expected error with closed client")
	}
}

func TestCreateDirectory_ClosedClient(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	fs := newTestFS()
	addr, cleanup := testSSHServerWithFS(t, signer.PublicKey(), fs)
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
	client.Close()

	err = CreateDirectory(client, "/root/newdir")
	if err == nil {
		t.Error("expected error with closed client")
	}
}

// shellQuote additional edge cases

func TestShellQuote_SpecialCharacters(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/root/file with spaces/test.txt", "'/root/file with spaces/test.txt'"},
		{"path'with'quotes", "'path'\\''with'\\''quotes'"},
		{"hello\nworld", "'hello\nworld'"},
		{"tab\there", "'tab\there'"},
		{"$HOME/.config", "'$HOME/.config'"},
		{"`command`", "'`command`'"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// Write-then-read round trip

func TestWriteAndReadRoundTrip(t *testing.T) {
	fs := newTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	content := []byte("round trip content: café, 日本語, line2\n")
	err := WriteFile(client, "/root/roundtrip.txt", content)
	if err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	data, err := ReadFile(client, "/root/roundtrip.txt")
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("round trip mismatch: got %q, want %q", string(data), string(content))
	}
}

func TestWriteAndListRoundTrip(t *testing.T) {
	fs := newRichTestFS()
	client, cleanup := newTestClient(t, fs)
	defer cleanup()

	// Create directory and write file, then verify via listing
	err := CreateDirectory(client, "/root/created")
	if err != nil {
		t.Fatalf("CreateDirectory error: %v", err)
	}

	err = WriteFile(client, "/root/created/doc.txt", []byte("created doc"))
	if err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	entries, err := ListDirectory(client, "/root/created")
	if err != nil {
		t.Fatalf("ListDirectory error: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "doc.txt" {
			found = true
			if e.Type != "file" {
				t.Errorf("expected type 'file', got %q", e.Type)
			}
		}
	}
	if !found {
		t.Error("doc.txt not found in listing after write")
	}
}
