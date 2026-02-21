//go:build docker_integration

package sshfiles

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

const (
	sshReadyTimeout      = 90 * time.Second
	sshReadyPollInterval = 1 * time.Second
)

// getExternalAgentInfo reads container info from environment variables set by
// the TypeScript test harness (tests/ssh/file.test.ts). If any required
// variable is missing, the test is skipped.
func getExternalAgentInfo(t *testing.T) (containerID, sshHost string, sshPort int) {
	t.Helper()
	containerID = os.Getenv("AGENT_CONTAINER_ID")
	sshHost = os.Getenv("AGENT_SSH_HOST")
	portStr := os.Getenv("AGENT_SSH_PORT")
	if containerID == "" || sshHost == "" || portStr == "" {
		t.Skip("External agent not configured (set AGENT_CONTAINER_ID, AGENT_SSH_HOST, AGENT_SSH_PORT)")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("invalid AGENT_SSH_PORT %q: %v", portStr, err)
	}
	return containerID, sshHost, port
}

// waitForSSHD waits until sshd is accepting connections with a valid banner.
func waitForSSHD(t *testing.T, host string, port int) {
	t.Helper()
	deadline := time.Now().Add(sshReadyTimeout)
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			time.Sleep(sshReadyPollInterval)
			continue
		}
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 256)
		n, err := conn.Read(buf)
		conn.Close()
		if err == nil && n > 0 && strings.HasPrefix(string(buf[:n]), "SSH-") {
			t.Logf("sshd ready at %s (banner: %s)", addr, strings.TrimSpace(string(buf[:n])))
			return
		}
		time.Sleep(sshReadyPollInterval)
	}
	t.Fatalf("sshd not ready at %s after %v", addr, sshReadyTimeout)
}

// uploadPublicKeyViaDocker installs a public key on the container using docker exec.
func uploadPublicKeyViaDocker(t *testing.T, containerID, publicKey string) {
	t.Helper()

	// Create .ssh directory
	cmd := exec.Command("docker", "exec", containerID, "sh", "-c", "mkdir -p /root/.ssh && chmod 700 /root/.ssh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create .ssh dir: %v\n%s", err, out)
	}

	// Write public key via base64 to safely pass content
	b64 := base64.StdEncoding.EncodeToString([]byte(publicKey))
	writeCmd := fmt.Sprintf("echo '%s' | base64 -d > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys", b64)
	cmd = exec.Command("docker", "exec", containerID, "sh", "-c", writeCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write authorized_keys: %v\n%s", err, out)
	}
}

// connectSSH establishes an SSH connection to the container using the given signer.
func connectSSH(t *testing.T, host string, port int, signer ssh.Signer) *ssh.Client {
	t.Helper()
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatalf("ssh dial %s: %v", addr, err)
	}
	return client
}

// setupExternalSSH sets up an SSH connection to an externally managed container.
// Returns the SSH client and a cleanup function.
func setupExternalSSH(t *testing.T) *ssh.Client {
	t.Helper()

	containerID, sshHost, sshPort := getExternalAgentInfo(t)
	waitForSSHD(t, sshHost, sshPort)

	// Generate a key pair
	_, privKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	signer, err := sshproxy.ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	// Upload public key to the container
	pubKeyStr := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	uploadPublicKeyViaDocker(t, containerID, pubKeyStr)

	// Connect
	client := connectSSH(t, sshHost, sshPort, signer)
	t.Cleanup(func() { client.Close() })

	return client
}

// TestExternalIntegration_ListDirectory tests listing directories on a real agent container.
func TestExternalIntegration_ListDirectory(t *testing.T) {
	client := setupExternalSSH(t)

	t.Run("root_directory", func(t *testing.T) {
		entries, err := ListDirectory(client, "/root")
		if err != nil {
			t.Fatalf("ListDirectory /root: %v", err)
		}
		t.Logf("Listed /root: %d entries", len(entries))
		if len(entries) == 0 {
			t.Error("expected at least 1 entry in /root")
		}
	})

	t.Run("tmp_directory", func(t *testing.T) {
		entries, err := ListDirectory(client, "/tmp")
		if err != nil {
			t.Fatalf("ListDirectory /tmp: %v", err)
		}
		t.Logf("Listed /tmp: %d entries", len(entries))
	})

	t.Run("etc_directory", func(t *testing.T) {
		entries, err := ListDirectory(client, "/etc")
		if err != nil {
			t.Fatalf("ListDirectory /etc: %v", err)
		}
		if len(entries) == 0 {
			t.Error("expected entries in /etc")
		}
		// /etc should contain well-known files
		foundHostname := false
		for _, e := range entries {
			if e.Name == "hostname" {
				foundHostname = true
			}
		}
		if !foundHostname {
			t.Error("hostname not found in /etc")
		}
		t.Logf("Listed /etc: %d entries", len(entries))
	})

	t.Run("nonexistent_directory", func(t *testing.T) {
		_, err := ListDirectory(client, "/nonexistent_dir_12345")
		if err == nil {
			t.Error("expected error for non-existent directory")
		}
	})
}

// TestExternalIntegration_ReadFile tests reading files on a real agent container.
func TestExternalIntegration_ReadFile(t *testing.T) {
	client := setupExternalSSH(t)

	t.Run("etc_hostname", func(t *testing.T) {
		data, err := ReadFile(client, "/etc/hostname")
		if err != nil {
			t.Fatalf("ReadFile /etc/hostname: %v", err)
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			t.Error("hostname file is empty")
		}
		t.Logf("Hostname: %s", content)
	})

	t.Run("etc_os_release", func(t *testing.T) {
		data, err := ReadFile(client, "/etc/os-release")
		if err != nil {
			t.Fatalf("ReadFile /etc/os-release: %v", err)
		}
		content := string(data)
		if !strings.Contains(content, "Ubuntu") && !strings.Contains(content, "ID=") {
			t.Errorf("unexpected os-release content: %s", content[:min(len(content), 200)])
		}
		t.Logf("OS release: %d bytes", len(data))
	})

	t.Run("nonexistent_file", func(t *testing.T) {
		_, err := ReadFile(client, "/nonexistent_file_12345")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})
}

// TestExternalIntegration_WriteAndReadFile tests write-then-read round trips.
func TestExternalIntegration_WriteAndReadFile(t *testing.T) {
	client := setupExternalSSH(t)

	t.Run("text_file", func(t *testing.T) {
		content := []byte("Hello from integration test!\nLine 2\n")
		path := "/tmp/sshfiles_test_text.txt"

		if err := WriteFile(client, path, content); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		data, err := ReadFile(client, path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("round trip mismatch:\ngot:  %q\nwant: %q", string(data), string(content))
		}
		t.Logf("Text file round trip OK (%d bytes)", len(content))
	})

	t.Run("json_content", func(t *testing.T) {
		content := []byte(`{"name": "test", "values": [1, 2, 3], "nested": {"key": "value"}}`)
		path := "/tmp/sshfiles_test_config.json"

		if err := WriteFile(client, path, content); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		data, err := ReadFile(client, path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("JSON round trip mismatch:\ngot:  %q\nwant: %q", string(data), string(content))
		}
		t.Log("JSON round trip OK")
	})

	t.Run("unicode_content", func(t *testing.T) {
		content := []byte("café résumé naïve ñ 日本語 中文 한국어")
		path := "/tmp/sshfiles_test_unicode.txt"

		if err := WriteFile(client, path, content); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		data, err := ReadFile(client, path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("unicode round trip mismatch:\ngot:  %q\nwant: %q", string(data), string(content))
		}
		t.Log("Unicode round trip OK")
	})

	t.Run("special_characters", func(t *testing.T) {
		content := []byte("line with 'quotes' and \"double quotes\"\ttabs\nand $variables ${VAR}")
		path := "/tmp/sshfiles_test_special.txt"

		if err := WriteFile(client, path, content); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		data, err := ReadFile(client, path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("special chars round trip mismatch:\ngot:  %q\nwant: %q", string(data), string(content))
		}
		t.Log("Special characters round trip OK")
	})

	t.Run("empty_file", func(t *testing.T) {
		path := "/tmp/sshfiles_test_empty.txt"

		if err := WriteFile(client, path, []byte{}); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		data, err := ReadFile(client, path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if len(data) != 0 {
			t.Errorf("expected empty file, got %d bytes", len(data))
		}
		t.Log("Empty file round trip OK")
	})

	t.Run("binary_content", func(t *testing.T) {
		// All 256 byte values
		content := make([]byte, 256)
		for i := range content {
			content[i] = byte(i)
		}
		path := "/tmp/sshfiles_test_binary.bin"

		if err := WriteFile(client, path, content); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		// Use SSH cat to read back — note that binary files with null bytes
		// may not survive cat intact. This tests the base64 chunked write path.
		// Verify using ls -la size instead.
		stdout, _, exitCode, err := executeCommand(client, fmt.Sprintf("wc -c < %s", shellQuote(path)))
		if err != nil {
			t.Fatalf("wc -c: %v", err)
		}
		if exitCode != 0 {
			t.Fatalf("wc -c exit code %d", exitCode)
		}
		sizeStr := strings.TrimSpace(stdout)
		if sizeStr != "256" {
			t.Errorf("expected file size 256, got %s", sizeStr)
		}
		t.Log("Binary content write OK (verified size)")
	})
}

// TestExternalIntegration_LargeFile tests writing and verifying a large file (>1MB).
func TestExternalIntegration_LargeFile(t *testing.T) {
	client := setupExternalSSH(t)

	// Generate 1MB of random data
	size := 1024 * 1024
	content := make([]byte, size)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("generate random data: %v", err)
	}

	path := "/tmp/sshfiles_test_large.bin"

	start := time.Now()
	if err := WriteFile(client, path, content); err != nil {
		t.Fatalf("WriteFile large: %v", err)
	}
	writeDuration := time.Since(start)
	t.Logf("Wrote %d bytes in %s", size, writeDuration)

	// Verify size
	stdout, _, exitCode, err := executeCommand(client, fmt.Sprintf("wc -c < %s", shellQuote(path)))
	if err != nil {
		t.Fatalf("wc -c: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("wc -c exit code %d", exitCode)
	}
	sizeStr := strings.TrimSpace(stdout)
	expectedSize := fmt.Sprintf("%d", size)
	if sizeStr != expectedSize {
		t.Errorf("expected file size %s, got %s", expectedSize, sizeStr)
	}

	// Verify content integrity using md5sum
	stdout, _, exitCode, err = executeCommand(client, fmt.Sprintf("md5sum %s", shellQuote(path)))
	if err != nil {
		t.Fatalf("md5sum: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("md5sum exit code %d", exitCode)
	}
	remoteMD5 := strings.Fields(strings.TrimSpace(stdout))[0]

	// Compute local MD5 by writing to a temp file and reading back via base64
	// to compare. Instead, re-read the file via base64 and compare.
	stdout, _, exitCode, err = executeCommand(client, fmt.Sprintf("base64 %s", shellQuote(path)))
	if err != nil {
		t.Fatalf("base64 read: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("base64 read exit code %d", exitCode)
	}
	readBack, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !bytes.Equal(readBack, content) {
		t.Errorf("large file content mismatch (remote md5: %s)", remoteMD5)
	}
	t.Logf("Large file integrity verified: %d bytes, write %s", size, writeDuration)
}

// TestExternalIntegration_CreateDirectory tests directory creation on a real agent.
func TestExternalIntegration_CreateDirectory(t *testing.T) {
	client := setupExternalSSH(t)

	t.Run("simple_directory", func(t *testing.T) {
		path := "/tmp/sshfiles_test_dir"
		if err := CreateDirectory(client, path); err != nil {
			t.Fatalf("CreateDirectory: %v", err)
		}

		// Verify it exists
		_, _, exitCode, err := executeCommand(client, fmt.Sprintf("test -d %s && echo ok", shellQuote(path)))
		if err != nil {
			t.Fatalf("verify dir: %v", err)
		}
		if exitCode != 0 {
			t.Error("directory was not created")
		}
		t.Log("Simple directory created OK")
	})

	t.Run("nested_directories", func(t *testing.T) {
		path := "/tmp/sshfiles_test_nested/a/b/c"
		if err := CreateDirectory(client, path); err != nil {
			t.Fatalf("CreateDirectory nested: %v", err)
		}

		// Verify all levels exist
		for _, p := range []string{
			"/tmp/sshfiles_test_nested",
			"/tmp/sshfiles_test_nested/a",
			"/tmp/sshfiles_test_nested/a/b",
			"/tmp/sshfiles_test_nested/a/b/c",
		} {
			_, _, exitCode, err := executeCommand(client, fmt.Sprintf("test -d %s && echo ok", shellQuote(p)))
			if err != nil {
				t.Fatalf("verify dir %s: %v", p, err)
			}
			if exitCode != 0 {
				t.Errorf("directory %s was not created", p)
			}
		}
		t.Log("Nested directories created OK")
	})

	t.Run("existing_directory_idempotent", func(t *testing.T) {
		// mkdir -p should succeed even if directory exists
		if err := CreateDirectory(client, "/tmp"); err != nil {
			t.Fatalf("CreateDirectory /tmp: %v", err)
		}
		t.Log("Idempotent mkdir OK")
	})
}

// TestExternalIntegration_CreateDirThenWriteAndList tests the full workflow:
// create a directory, write a file into it, then list and read it back.
func TestExternalIntegration_CreateDirThenWriteAndList(t *testing.T) {
	client := setupExternalSSH(t)

	dir := "/tmp/sshfiles_test_workflow"
	filePath := dir + "/document.txt"
	content := []byte("integration test document content")

	// Create directory
	if err := CreateDirectory(client, dir); err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}

	// Write file
	if err := WriteFile(client, filePath, content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// List directory
	entries, err := ListDirectory(client, dir)
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "document.txt" {
			found = true
			if e.Type != "file" {
				t.Errorf("expected type 'file', got %q", e.Type)
			}
		}
	}
	if !found {
		t.Error("document.txt not found in directory listing")
	}

	// Read file
	data, err := ReadFile(client, filePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content mismatch:\ngot:  %q\nwant: %q", string(data), string(content))
	}

	t.Log("Full workflow (mkdir → write → list → read) OK")
}

// TestExternalIntegration_PermissionDenied tests error handling for permission-denied scenarios.
func TestExternalIntegration_PermissionDenied(t *testing.T) {
	client := setupExternalSSH(t)

	// The agent runs as root, so most paths are accessible.
	// Write to a read-only filesystem path if available.
	t.Run("write_to_proc", func(t *testing.T) {
		err := WriteFile(client, "/proc/test_file", []byte("data"))
		if err == nil {
			t.Error("expected error writing to /proc")
		}
		t.Logf("Write to /proc correctly failed: %v", err)
	})
}

// TestExternalIntegration_FileOverwrite tests that writing to an existing file overwrites it.
func TestExternalIntegration_FileOverwrite(t *testing.T) {
	client := setupExternalSSH(t)

	path := "/tmp/sshfiles_test_overwrite.txt"

	// Write original content
	original := []byte("original content here")
	if err := WriteFile(client, path, original); err != nil {
		t.Fatalf("WriteFile original: %v", err)
	}

	// Overwrite with new content
	updated := []byte("updated content")
	if err := WriteFile(client, path, updated); err != nil {
		t.Fatalf("WriteFile updated: %v", err)
	}

	// Read back
	data, err := ReadFile(client, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(updated) {
		t.Errorf("expected updated content, got %q", string(data))
	}
	t.Log("File overwrite OK")
}
