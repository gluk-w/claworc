package sshfiles

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// ==========================================================================
// Integration tests for SSH-based file operations.
// Covers: directory listing at various paths, text/binary reads,
// file creation with various content types, nested directory creation,
// error messages, and large file (>10MB) streaming.
// ==========================================================================

// --- Directory listing at various paths ---

func TestListDirectory_RootPath(t *testing.T) {
	lsOutput := `total 24
drwx------  3 root root 4096 Feb 20 10:00 .
drwxr-xr-x 22 root root 4096 Feb 20 09:00 ..
-rw-r--r--  1 root root 3106 Feb 20 10:00 .bashrc
-rw-r--r--  1 root root  161 Feb 20 10:00 .profile
drwx------  2 root root 4096 Feb 20 10:00 .ssh
drwxr-xr-x  2 root root 4096 Feb 20 10:00 Documents
`
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "ls -la") && strings.Contains(cmd, "/root") {
			return lsOutput, "", 0
		}
		return "", "unexpected command", 1
	})
	defer cleanup()

	entries, err := ListDirectory(client, "/root")
	if err != nil {
		t.Fatalf("ListDirectory(/root): %v", err)
	}

	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// Verify .bashrc
	if entries[0].Name != ".bashrc" || entries[0].Type != "file" {
		t.Errorf("entry 0: expected .bashrc file, got %s %s", entries[0].Name, entries[0].Type)
	}
	// Verify .profile
	if entries[1].Name != ".profile" || entries[1].Type != "file" {
		t.Errorf("entry 1: expected .profile file, got %s %s", entries[1].Name, entries[1].Type)
	}
	// Verify .ssh directory
	if entries[2].Name != ".ssh" || entries[2].Type != "directory" {
		t.Errorf("entry 2: expected .ssh directory, got %s %s", entries[2].Name, entries[2].Type)
	}
	// Verify Documents directory
	if entries[3].Name != "Documents" || entries[3].Type != "directory" {
		t.Errorf("entry 3: expected Documents directory, got %s %s", entries[3].Name, entries[3].Type)
	}
}

func TestListDirectory_TmpPath(t *testing.T) {
	lsOutput := `total 12
drwxrwxrwt  3 root root 4096 Feb 20 10:00 .
drwxr-xr-x 22 root root 4096 Feb 20 09:00 ..
-rw-------  1 root root  512 Feb 20 10:00 tmpfile.dat
drwxrwxrwt  2 root root 4096 Feb 20 10:00 systemd-private-abc
`
	var receivedPath string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "ls -la") {
			receivedPath = cmd
			return lsOutput, "", 0
		}
		return "", "unexpected command", 1
	})
	defer cleanup()

	entries, err := ListDirectory(client, "/tmp")
	if err != nil {
		t.Fatalf("ListDirectory(/tmp): %v", err)
	}
	if !strings.Contains(receivedPath, "/tmp") {
		t.Errorf("expected command to contain /tmp, got %q", receivedPath)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "tmpfile.dat" {
		t.Errorf("expected tmpfile.dat, got %s", entries[0].Name)
	}
	if entries[0].Permissions != "-rw-------" {
		t.Errorf("expected -rw-------, got %s", entries[0].Permissions)
	}
	if entries[1].Name != "systemd-private-abc" || entries[1].Type != "directory" {
		t.Errorf("expected systemd-private-abc directory, got %s %s", entries[1].Name, entries[1].Type)
	}
}

func TestListDirectory_EtcPath(t *testing.T) {
	lsOutput := `total 200
drwxr-xr-x 50 root root  4096 Feb 20 10:00 .
drwxr-xr-x 22 root root  4096 Feb 20 09:00 ..
-rw-r--r--  1 root root  2981 Feb 20 10:00 adduser.conf
drwxr-xr-x  3 root root  4096 Feb 20 10:00 apt
-rw-r--r--  1 root root   367 Feb 20 10:00 hosts
-rw-r--r--  1 root root   104 Feb 20 10:00 hostname
lrwxrwxrwx  1 root root    21 Feb 20 10:00 mtab -> /proc/self/mounts
-rw-r-----  1 root shadow  920 Feb 20 10:00 shadow
`
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "ls -la") && strings.Contains(cmd, "/etc") {
			return lsOutput, "", 0
		}
		return "", "unexpected command", 1
	})
	defer cleanup()

	entries, err := ListDirectory(client, "/etc")
	if err != nil {
		t.Fatalf("ListDirectory(/etc): %v", err)
	}

	if len(entries) != 6 {
		t.Fatalf("expected 6 entries, got %d", len(entries))
	}

	// Verify symlink
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name, "mtab") {
			found = true
			if e.Type != "symlink" {
				t.Errorf("expected mtab to be symlink, got %s", e.Type)
			}
		}
	}
	if !found {
		t.Error("mtab symlink not found in entries")
	}

	// Verify directory
	for _, e := range entries {
		if e.Name == "apt" {
			if e.Type != "directory" {
				t.Errorf("expected apt to be directory, got %s", e.Type)
			}
			if e.Size != nil {
				t.Errorf("expected nil size for directory apt, got %v", e.Size)
			}
		}
	}
}

func TestListDirectory_PathWithSpaces(t *testing.T) {
	lsOutput := `total 4
drwxr-xr-x 2 root root 4096 Feb 20 10:00 .
drwxr-xr-x 3 root root 4096 Feb 20 09:00 ..
-rw-r--r-- 1 root root  100 Feb 20 10:00 readme.txt
`
	var receivedCmd string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		receivedCmd = cmd
		return lsOutput, "", 0
	})
	defer cleanup()

	entries, err := ListDirectory(client, "/root/my documents")
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// Verify the path was properly shell-quoted
	if !strings.Contains(receivedCmd, "'") {
		t.Errorf("expected shell quoting in command, got %q", receivedCmd)
	}
}

func TestListDirectory_PermissionDenied(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "ls: cannot open directory '/root/private': Permission denied", 2
	})
	defer cleanup()

	_, err := ListDirectory(client, "/root/private")
	if err == nil {
		t.Fatal("expected error for permission denied")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("expected 'Permission denied' in error, got: %v", err)
	}
}

// --- Reading text and binary files ---

func TestReadFile_TextWithUTF8(t *testing.T) {
	content := "Hello, 世界! Привет мир! 🌍\nLine 2: café résumé\n"
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return content, "", 0
		}
		return "", "unexpected", 1
	})
	defer cleanup()

	data, err := ReadFile(client, "/root/utf8.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != content {
		t.Errorf("content mismatch: expected %q, got %q", content, string(data))
	}
}

func TestReadFile_JSONContent(t *testing.T) {
	content := `{"name":"test","values":[1,2,3],"nested":{"key":"value"}}`
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return content, "", 0
		}
		return "", "unexpected", 1
	})
	defer cleanup()

	data, err := ReadFile(client, "/root/config.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestReadFile_BinaryPNG(t *testing.T) {
	// Minimal PNG header bytes
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	// Add some random binary data after the header
	binaryData := append(pngHeader, bytes.Repeat([]byte{0x00, 0xFF, 0xAB, 0xCD}, 256)...)

	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return string(binaryData), "", 0
		}
		return "", "unexpected", 1
	})
	defer cleanup()

	data, err := ReadFile(client, "/root/image.png")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, binaryData) {
		t.Errorf("binary data mismatch: expected %d bytes, got %d bytes", len(binaryData), len(data))
	}
}

func TestReadFile_EmptyFile(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer cleanup()

	data, err := ReadFile(client, "/root/empty.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty content, got %d bytes", len(data))
	}
}

func TestReadFile_IsDirectory(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "cat: /root/Documents: Is a directory", 1
	})
	defer cleanup()

	_, err := ReadFile(client, "/root/Documents")
	if err == nil {
		t.Fatal("expected error for reading a directory")
	}
	if !strings.Contains(err.Error(), "Is a directory") {
		t.Errorf("expected 'Is a directory' error, got: %v", err)
	}
}

// --- Creating new files with various content types ---

func TestWriteFile_JSONContent(t *testing.T) {
	var receivedData string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedData = string(data)
		return "", "", 0
	})
	defer cleanup()

	jsonContent := `{"config":{"debug":true,"port":8080},"version":"1.0.0"}`
	err := WriteFile(client, "/root/config.json", []byte(jsonContent))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if receivedData != jsonContent {
		t.Errorf("expected %q, got %q", jsonContent, receivedData)
	}
}

func TestWriteFile_HTMLContent(t *testing.T) {
	var receivedData string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedData = string(data)
		return "", "", 0
	})
	defer cleanup()

	htmlContent := `<!DOCTYPE html>
<html>
<head><title>Test</title></head>
<body><h1>Hello & welcome</h1></body>
</html>`
	err := WriteFile(client, "/root/index.html", []byte(htmlContent))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if receivedData != htmlContent {
		t.Errorf("expected %q, got %q", htmlContent, receivedData)
	}
}

func TestWriteFile_ScriptWithSpecialChars(t *testing.T) {
	var receivedData string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedData = string(data)
		return "", "", 0
	})
	defer cleanup()

	script := "#!/bin/bash\necho \"Hello $USER\"\nif [ -f /etc/hosts ]; then\n  cat /etc/hosts | grep 'localhost'\nfi\n"
	err := WriteFile(client, "/root/script.sh", []byte(script))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if receivedData != script {
		t.Errorf("script content mismatch")
	}
}

func TestWriteFile_BinaryContent(t *testing.T) {
	binaryData := make([]byte, 1024)
	for i := range binaryData {
		binaryData[i] = byte(i % 256)
	}

	var receivedLen int
	var receivedData []byte
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedLen = len(data)
		receivedData = data
		return "", "", 0
	})
	defer cleanup()

	err := WriteFile(client, "/root/binary.dat", binaryData)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if receivedLen != 1024 {
		t.Errorf("expected 1024 bytes, got %d", receivedLen)
	}
	if !bytes.Equal(receivedData, binaryData) {
		t.Error("binary content mismatch")
	}
}

func TestWriteFile_PathWithSpecialChars(t *testing.T) {
	var receivedCmd string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		receivedCmd = cmd
		io.ReadAll(stdin)
		return "", "", 0
	})
	defer cleanup()

	err := WriteFile(client, "/root/file with spaces.txt", []byte("content"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !strings.Contains(receivedCmd, "file with spaces.txt") {
		t.Errorf("expected path with spaces in command, got %q", receivedCmd)
	}
}

func TestWriteFile_NoSpaceLeft(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		io.ReadAll(stdin)
		return "", "No space left on device", 1
	})
	defer cleanup()

	err := WriteFile(client, "/root/big.dat", []byte("data"))
	if err == nil {
		t.Fatal("expected error for no space left")
	}
	if !strings.Contains(err.Error(), "No space left on device") {
		t.Errorf("expected 'No space left on device' error, got: %v", err)
	}
}

// --- Creating directories with nested paths ---

func TestCreateDirectory_DeeplyNested(t *testing.T) {
	var receivedCmd string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		receivedCmd = cmd
		return "", "", 0
	})
	defer cleanup()

	err := CreateDirectory(client, "/root/a/b/c/d/e/f/g/h")
	if err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}
	if !strings.Contains(receivedCmd, "mkdir -p") {
		t.Errorf("expected mkdir -p, got %q", receivedCmd)
	}
	if !strings.Contains(receivedCmd, "/root/a/b/c/d/e/f/g/h") {
		t.Errorf("expected nested path, got %q", receivedCmd)
	}
}

func TestCreateDirectory_PathWithSpaces(t *testing.T) {
	var receivedCmd string
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		receivedCmd = cmd
		return "", "", 0
	})
	defer cleanup()

	err := CreateDirectory(client, "/root/my project/src/components")
	if err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}
	if !strings.Contains(receivedCmd, "my project") {
		t.Errorf("expected path with spaces, got %q", receivedCmd)
	}
}

func TestCreateDirectory_AlreadyExists(t *testing.T) {
	// mkdir -p doesn't error if directory already exists
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "", 0
	})
	defer cleanup()

	err := CreateDirectory(client, "/tmp")
	if err != nil {
		t.Fatalf("CreateDirectory on existing dir should succeed: %v", err)
	}
}

func TestCreateDirectory_ReadOnlyFS(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "mkdir: cannot create directory '/readonly/dir': Read-only file system", 1
	})
	defer cleanup()

	err := CreateDirectory(client, "/readonly/dir")
	if err == nil {
		t.Fatal("expected error for read-only filesystem")
	}
	if !strings.Contains(err.Error(), "Read-only file system") {
		t.Errorf("expected 'Read-only file system' error, got: %v", err)
	}
}

// --- Error message verification ---

func TestListDirectory_NotADirectory(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "ls: cannot access '/root/file.txt': Not a directory", 2
	})
	defer cleanup()

	_, err := ListDirectory(client, "/root/file.txt")
	if err == nil {
		t.Fatal("expected error for non-directory")
	}
	if !strings.Contains(err.Error(), "Not a directory") {
		t.Errorf("expected 'Not a directory' error, got: %v", err)
	}
}

func TestReadFile_SymlinkLoop(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "cat: /root/looplink: Too many levels of symbolic links", 1
	})
	defer cleanup()

	_, err := ReadFile(client, "/root/looplink")
	if err == nil {
		t.Fatal("expected error for symlink loop")
	}
	if !strings.Contains(err.Error(), "Too many levels of symbolic links") {
		t.Errorf("expected symlink loop error, got: %v", err)
	}
}

func TestWriteFile_PermissionDenied(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		io.ReadAll(stdin)
		return "", "Permission denied", 1
	})
	defer cleanup()

	err := WriteFile(client, "/etc/shadow", []byte("data"))
	if err == nil {
		t.Fatal("expected error for permission denied")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("expected 'Permission denied' error, got: %v", err)
	}
}

func TestCreateDirectory_ParentNotWritable(t *testing.T) {
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "mkdir: cannot create directory '/noperm/sub': Permission denied", 1
	})
	defer cleanup()

	err := CreateDirectory(client, "/noperm/sub")
	if err == nil {
		t.Fatal("expected error for non-writable parent")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("expected 'Permission denied' error, got: %v", err)
	}
}

// --- Large file tests (>10MB) ---

func TestWriteFile_LargeFile10MB(t *testing.T) {
	// 10MB of data
	largeData := bytes.Repeat([]byte("ABCDEFGHIJ"), 1024*1024)
	if len(largeData) != 10*1024*1024 {
		t.Fatalf("expected 10MB data, got %d bytes", len(largeData))
	}

	var receivedLen int
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedLen = len(data)
		return "", "", 0
	})
	defer cleanup()

	err := WriteFile(client, "/root/large10mb.bin", largeData)
	if err != nil {
		t.Fatalf("WriteFile (10MB): %v", err)
	}
	if receivedLen != len(largeData) {
		t.Errorf("expected %d bytes, got %d", len(largeData), receivedLen)
	}
}

func TestReadFile_LargeFile10MB(t *testing.T) {
	// 10MB of data
	largeContent := string(bytes.Repeat([]byte("X"), 10*1024*1024))

	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return largeContent, "", 0
		}
		return "", "unexpected", 1
	})
	defer cleanup()

	data, err := ReadFile(client, "/root/large10mb.bin")
	if err != nil {
		t.Fatalf("ReadFile (10MB): %v", err)
	}
	if len(data) != 10*1024*1024 {
		t.Errorf("expected %d bytes, got %d", 10*1024*1024, len(data))
	}
}

func TestWriteFile_LargeFile12MB(t *testing.T) {
	// 12MB of mixed binary data
	largeData := make([]byte, 12*1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	var receivedLen int
	var dataMatches bool
	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		data, _ := io.ReadAll(stdin)
		receivedLen = len(data)
		dataMatches = bytes.Equal(data, largeData)
		return "", "", 0
	})
	defer cleanup()

	err := WriteFile(client, "/root/large12mb.bin", largeData)
	if err != nil {
		t.Fatalf("WriteFile (12MB): %v", err)
	}
	if receivedLen != len(largeData) {
		t.Errorf("expected %d bytes, got %d", len(largeData), receivedLen)
	}
	if !dataMatches {
		t.Error("12MB binary data content mismatch")
	}
}

// --- Round-trip integration tests ---

func TestRoundTrip_MultipleFiles(t *testing.T) {
	fs := make(map[string][]byte)

	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.HasPrefix(cmd, "cat > ") {
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

	files := map[string]string{
		"/root/file1.txt":      "content of file 1",
		"/root/file2.json":     `{"key": "value"}`,
		"/root/deep/file3.sh":  "#!/bin/bash\necho hello\n",
		"/tmp/tempfile.dat":    "temporary data",
		"/root/empty_file.txt": "",
	}

	// Write all files
	for path, content := range files {
		if err := WriteFile(client, path, []byte(content)); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}

	// Read them back and verify
	for path, expected := range files {
		data, err := ReadFile(client, path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if string(data) != expected {
			t.Errorf("round-trip mismatch for %s: expected %q, got %q", path, expected, string(data))
		}
	}

	// Verify reading non-existent file fails
	_, err := ReadFile(client, "/root/nonexistent.txt")
	if err == nil {
		t.Error("expected error reading nonexistent file")
	}
}

func TestRoundTrip_BinaryData(t *testing.T) {
	fs := make(map[string][]byte)

	client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.HasPrefix(cmd, "cat > ") {
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
			return "", "not found", 1
		}
		return "", "unknown", 1
	})
	defer cleanup()

	// Create binary data with all possible byte values
	binaryData := make([]byte, 256)
	for i := range binaryData {
		binaryData[i] = byte(i)
	}

	if err := WriteFile(client, "/root/binary.dat", binaryData); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	readBack, err := ReadFile(client, "/root/binary.dat")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !bytes.Equal(readBack, binaryData) {
		t.Errorf("binary round-trip mismatch: expected %d bytes, got %d bytes", len(binaryData), len(readBack))
	}
}
