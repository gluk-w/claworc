package sshfiles

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// --- Test SSH server that handles exec requests ---

// commandHandler is called for each "exec" request. It receives the command
// string and returns (stdout, stderr, exitCode).
type commandHandler func(cmd string, stdin io.Reader) (stdout, stderr string, exitCode int)

// startExecSSHServer starts an SSH server that handles "exec" channel requests.
// The handler function processes each command and returns the response.
func startExecSSHServer(t *testing.T, handler commandHandler) (client *gossh.Client, cleanup func()) {
	t.Helper()

	// Generate server host key
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

	// Generate client key pair
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
			go handleSSHConn(conn, serverCfg, handler)
		}
	}()

	// Build client config
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

func handleSSHConn(netConn net.Conn, config *gossh.ServerConfig, handler commandHandler) {
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
		go handleSession(ch, requests, handler)
	}
}

func handleSession(ch gossh.Channel, reqs <-chan *gossh.Request, handler commandHandler) {
	defer ch.Close()

	for req := range reqs {
		switch req.Type {
		case "exec":
			if len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			// Payload format: uint32 length + string command
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

			// Send exit-status
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

// --- Helper: write a test private key to disk ---

func writeTestKey(t *testing.T, dir, name string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBlock, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPath := filepath.Join(dir, name+".key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return keyPath
}

// --- executeCommand tests ---

func TestExecuteCommandSuccess(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "hello world\n", "", 0
	})
	defer cleanup()

	stdout, stderr, exitCode, err := executeCommand(client, "echo hello world")
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if stdout != "hello world\n" {
		t.Errorf("expected stdout 'hello world\\n', got %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr, got %q", stderr)
	}
}

func TestExecuteCommandNonZeroExit(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "not found\n", 2
	})
	defer cleanup()

	stdout, stderr, exitCode, err := executeCommand(client, "ls /nonexistent")
	if err != nil {
		t.Fatalf("executeCommand: %v", err)
	}
	if exitCode != 2 {
		t.Errorf("expected exit code 2, got %d", exitCode)
	}
	if stdout != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if stderr != "not found\n" {
		t.Errorf("expected stderr 'not found\\n', got %q", stderr)
	}
}

func TestExecuteCommandWithStdin(t *testing.T) {
	var receivedStdin string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedStdin = string(data)
		return "", "", 0
	})
	defer cleanup()

	err := executeCommandWithStdin(client, "cat > /tmp/test.txt", []byte("file content here"))
	if err != nil {
		t.Fatalf("executeCommandWithStdin: %v", err)
	}
	if receivedStdin != "file content here" {
		t.Errorf("expected stdin 'file content here', got %q", receivedStdin)
	}
}

func TestExecuteCommandWithStdinError(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		io.ReadAll(stdin)
		return "", "permission denied\n", 1
	})
	defer cleanup()

	err := executeCommandWithStdin(client, "cat > /root/readonly.txt", []byte("data"))
	if err == nil {
		t.Fatal("expected error for failed command")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected permission denied error, got: %v", err)
	}
}

// --- ListDirectory tests ---

func TestListDirectory(t *testing.T) {
	lsOutput := `total 16
drwxr-xr-x  4 root root 4096 Jan 15 10:30 .
drwxr-xr-x 20 root root 4096 Jan 15 09:00 ..
-rw-r--r--  1 root root  220 Jan 15 10:30 .bashrc
drwxr-xr-x  2 root root 4096 Jan 15 10:30 Documents
lrwxrwxrwx  1 root root    7 Jan 15 10:30 link -> target
`
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "ls -la") {
			return lsOutput, "", 0
		}
		return "", "unknown command", 1
	})
	defer cleanup()

	entries, err := ListDirectory(client, "/root")
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// .bashrc - file
	if entries[0].Name != ".bashrc" {
		t.Errorf("expected .bashrc, got %s", entries[0].Name)
	}
	if entries[0].Type != "file" {
		t.Errorf("expected type file, got %s", entries[0].Type)
	}
	if entries[0].Size == nil || *entries[0].Size != "220" {
		t.Errorf("expected size 220, got %v", entries[0].Size)
	}
	if entries[0].Permissions != "-rw-r--r--" {
		t.Errorf("expected -rw-r--r--, got %s", entries[0].Permissions)
	}

	// Documents - directory
	if entries[1].Name != "Documents" {
		t.Errorf("expected Documents, got %s", entries[1].Name)
	}
	if entries[1].Type != "directory" {
		t.Errorf("expected type directory, got %s", entries[1].Type)
	}
	if entries[1].Size != nil {
		t.Errorf("expected nil size for directory, got %v", entries[1].Size)
	}

	// link -> target - symlink
	if !strings.HasPrefix(entries[2].Name, "link") {
		t.Errorf("expected name starting with 'link', got %s", entries[2].Name)
	}
	if entries[2].Type != "symlink" {
		t.Errorf("expected type symlink, got %s", entries[2].Type)
	}
}

func TestListDirectoryNotFound(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "ls: cannot access '/nonexistent': No such file or directory", 2
	})
	defer cleanup()

	_, err := ListDirectory(client, "/nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
	if !strings.Contains(err.Error(), "No such file or directory") {
		t.Errorf("expected 'No such file or directory' error, got: %v", err)
	}
}

func TestListDirectoryEmpty(t *testing.T) {
	lsOutput := `total 0
drwxr-xr-x  2 root root 4096 Jan 15 10:30 .
drwxr-xr-x 20 root root 4096 Jan 15 09:00 ..
`
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return lsOutput, "", 0
	})
	defer cleanup()

	entries, err := ListDirectory(client, "/tmp/empty")
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty dir, got %d", len(entries))
	}
}

// --- ReadFile tests ---

func TestReadFile(t *testing.T) {
	content := "hello world\nline 2\n"
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return content, "", 0
		}
		return "", "unknown", 1
	})
	defer cleanup()

	data, err := ReadFile(client, "/root/test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestReadFileNotFound(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "cat: /nonexistent: No such file or directory", 1
	})
	defer cleanup()

	_, err := ReadFile(client, "/nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "No such file or directory") {
		t.Errorf("expected 'No such file or directory' error, got: %v", err)
	}
}

func TestReadFileBinary(t *testing.T) {
	// Simulate binary content with null bytes
	binaryContent := string([]byte{0x00, 0x01, 0x02, 0xFF, 0xFE})
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return binaryContent, "", 0
	})
	defer cleanup()

	data, err := ReadFile(client, "/root/binary.dat")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, []byte(binaryContent)) {
		t.Errorf("binary content mismatch")
	}
}

func TestReadFilePermissionDenied(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "cat: /etc/shadow: Permission denied", 1
	})
	defer cleanup()

	_, err := ReadFile(client, "/etc/shadow")
	if err == nil {
		t.Fatal("expected error for permission denied")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("expected 'Permission denied' error, got: %v", err)
	}
}

// --- WriteFile tests ---

func TestWriteFile(t *testing.T) {
	var receivedData string
	var receivedCmd string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		receivedCmd = cmd
		data, _ := io.ReadAll(stdin)
		receivedData = string(data)
		return "", "", 0
	})
	defer cleanup()

	err := WriteFile(client, "/root/output.txt", []byte("new content"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if receivedData != "new content" {
		t.Errorf("expected stdin 'new content', got %q", receivedData)
	}
	if !strings.Contains(receivedCmd, "/root/output.txt") {
		t.Errorf("expected command to reference path, got %q", receivedCmd)
	}
}

func TestWriteFileEmpty(t *testing.T) {
	var receivedData string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedData = string(data)
		return "", "", 0
	})
	defer cleanup()

	err := WriteFile(client, "/root/empty.txt", []byte{})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if receivedData != "" {
		t.Errorf("expected empty stdin, got %q", receivedData)
	}
}

func TestWriteFileLarge(t *testing.T) {
	// 1MB of data to test larger writes
	largeData := bytes.Repeat([]byte("A"), 1024*1024)
	var receivedLen int
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedLen = len(data)
		return "", "", 0
	})
	defer cleanup()

	err := WriteFile(client, "/root/large.bin", largeData)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if receivedLen != len(largeData) {
		t.Errorf("expected %d bytes, got %d", len(largeData), receivedLen)
	}
}

func TestWriteFileError(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		io.ReadAll(stdin)
		return "", "Read-only file system", 1
	})
	defer cleanup()

	err := WriteFile(client, "/readonly/test.txt", []byte("data"))
	if err == nil {
		t.Fatal("expected error for read-only filesystem")
	}
	if !strings.Contains(err.Error(), "Read-only file system") {
		t.Errorf("expected 'Read-only file system' error, got: %v", err)
	}
}

// --- CreateDirectory tests ---

func TestCreateDirectory(t *testing.T) {
	var receivedCmd string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		receivedCmd = cmd
		return "", "", 0
	})
	defer cleanup()

	err := CreateDirectory(client, "/root/new/nested/dir")
	if err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}
	if !strings.Contains(receivedCmd, "mkdir -p") {
		t.Errorf("expected mkdir -p command, got %q", receivedCmd)
	}
	if !strings.Contains(receivedCmd, "/root/new/nested/dir") {
		t.Errorf("expected path in command, got %q", receivedCmd)
	}
}

func TestCreateDirectoryPermissionDenied(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "mkdir: cannot create directory '/protected': Permission denied", 1
	})
	defer cleanup()

	err := CreateDirectory(client, "/protected")
	if err == nil {
		t.Fatal("expected error for permission denied")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("expected 'Permission denied' error, got: %v", err)
	}
}

// --- shellQuote tests ---

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/root/test.txt", "'/root/test.txt'"},
		{"/path/with spaces/file.txt", "'/path/with spaces/file.txt'"},
		{"file'name.txt", "'file'\\''name.txt'"},
		{"", "''"},
		{"/normal/path", "'/normal/path'"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- Integration-style: round-trip write + read ---

func TestWriteAndReadRoundTrip(t *testing.T) {
	// Simulate a filesystem with a map
	fs := make(map[string][]byte)

	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.HasPrefix(cmd, "cat > ") {
			// Extract path from "cat > '/path'"
			path := extractQuotedPath(cmd, "cat > ")
			data, _ := io.ReadAll(stdin)
			fs[path] = data
			return "", "", 0
		}
		if strings.HasPrefix(cmd, "cat ") {
			path := extractQuotedPath(cmd, "cat ")
			if data, ok := fs[path]; ok {
				return string(data), "", 0
			}
			return "", fmt.Sprintf("cat: %s: No such file or directory", path), 1
		}
		return "", "unknown command", 1
	})
	defer cleanup()

	content := []byte("round trip content\nwith newlines\n")

	err := WriteFile(client, "/root/roundtrip.txt", content)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := ReadFile(client, "/root/roundtrip.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !bytes.Equal(data, content) {
		t.Errorf("round trip mismatch: wrote %q, read %q", content, data)
	}
}

// extractQuotedPath extracts a shell-quoted path from a command string.
func extractQuotedPath(cmd, prefix string) string {
	rest := strings.TrimPrefix(cmd, prefix)
	// Remove surrounding single quotes
	rest = strings.TrimPrefix(rest, "'")
	rest = strings.TrimSuffix(rest, "'")
	return rest
}
