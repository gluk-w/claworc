package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/middleware"
)

// ==========================================================================
// Handler-level integration tests for SSH-based file operations.
// Covers: directory listing at various paths, text/binary reads,
// file creation with various content types, nested directory creation,
// multipart upload, download with headers, error messages, and large files.
// ==========================================================================

// --- BrowseFiles: directory listing at various paths ---

func TestBrowseFiles_EtcPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse-etc", "Browse Etc")
	admin := createTestAdmin(t)

	etcOutput := `total 200
drwxr-xr-x 50 root root  4096 Feb 20 10:00 .
drwxr-xr-x 22 root root  4096 Feb 20 09:00 ..
-rw-r--r--  1 root root   367 Feb 20 10:00 hosts
-rw-r--r--  1 root root   104 Feb 20 10:00 hostname
drwxr-xr-x  3 root root  4096 Feb 20 10:00 apt
lrwxrwxrwx  1 root root    21 Feb 20 10:00 mtab -> /proc/self/mounts
`
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "ls -la") && strings.Contains(cmd, "/etc") {
			return etcOutput, "", 0
		}
		return "", "unexpected command", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-browse-etc", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files?path=/etc", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["path"] != "/etc" {
		t.Errorf("expected path /etc, got %v", resp["path"])
	}
	entries := resp["entries"].([]interface{})
	if len(entries) != 4 {
		t.Errorf("expected 4 entries, got %d", len(entries))
	}

	// Verify entry types
	foundSymlink := false
	foundDir := false
	for _, e := range entries {
		entry := e.(map[string]interface{})
		if entry["type"] == "symlink" {
			foundSymlink = true
		}
		if entry["type"] == "directory" {
			foundDir = true
		}
	}
	if !foundSymlink {
		t.Error("expected to find a symlink entry")
	}
	if !foundDir {
		t.Error("expected to find a directory entry")
	}
}

func TestBrowseFiles_ManyEntries(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-browse-many", "Browse Many")
	admin := createTestAdmin(t)

	// Build an ls output with many files
	var sb strings.Builder
	sb.WriteString("total 100\ndrwxr-xr-x 2 root root 4096 Feb 20 10:00 .\ndrwxr-xr-x 3 root root 4096 Feb 20 09:00 ..\n")
	for i := 0; i < 50; i++ {
		sb.WriteString(fmt.Sprintf("-rw-r--r-- 1 root root %d Feb 20 10:00 file_%03d.txt\n", 100+i, i))
	}

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "ls -la") {
			return sb.String(), "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-browse-many", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files?path=/root/project", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	BrowseFiles(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	entries := resp["entries"].([]interface{})
	if len(entries) != 50 {
		t.Errorf("expected 50 entries, got %d", len(entries))
	}
}

// --- ReadFileContent: text and binary ---

func TestReadFileContent_UTF8Text(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-read-utf8", "Read UTF8")
	admin := createTestAdmin(t)

	utf8Content := "Hello, 世界! café résumé\n"
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return utf8Content, "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-read-utf8", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/read?path=/root/utf8.txt", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["content"] != utf8Content {
		t.Errorf("content mismatch: expected %q, got %q", utf8Content, resp["content"])
	}
}

func TestReadFileContent_EmptyFile(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-read-empty", "Read Empty")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-read-empty", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/read?path=/root/empty.txt", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["content"] != "" {
		t.Errorf("expected empty content, got %q", resp["content"])
	}
}

func TestReadFileContent_SSHErrorPermissionDenied(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-read-denied", "Read Denied")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "cat: /etc/shadow: Permission denied", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-read-denied", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/read?path=/etc/shadow", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp["detail"], "Permission denied") {
		t.Errorf("expected 'Permission denied' in error, got %q", resp["detail"])
	}
}

func TestReadFileContent_FileNotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-read-notfound", "Read NotFound")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "cat: /root/missing.txt: No such file or directory", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-read-notfound", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/read?path=/root/missing.txt", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	ReadFileContent(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp["detail"], "No such file or directory") {
		t.Errorf("expected 'No such file or directory' in error, got %q", resp["detail"])
	}
}

// --- DownloadFile: content-type headers ---

func TestDownloadFile_ContentHeaders(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-dl-headers", "DL Headers")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return "file content here", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-dl-headers", sshClient)
	defer smCleanup()

	tests := []struct {
		name     string
		path     string
		filename string
	}{
		{"txt file", "/root/readme.txt", "readme.txt"},
		{"json file", "/root/config.json", "config.json"},
		{"sh script", "/root/run.sh", "run.sh"},
		{"png image", "/root/image.png", "image.png"},
		{"nested path", "/root/deep/dir/file.log", "file.log"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/download?path=%s", inst.ID, tt.path),
				map[string]string{"id": fmt.Sprint(inst.ID)})
			r = middleware.WithUserForTest(r, admin)
			w := httptest.NewRecorder()
			DownloadFile(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}

			ct := w.Header().Get("Content-Type")
			if ct != "application/octet-stream" {
				t.Errorf("expected Content-Type application/octet-stream, got %q", ct)
			}

			cd := w.Header().Get("Content-Disposition")
			expectedCD := fmt.Sprintf(`attachment; filename="%s"`, tt.filename)
			if cd != expectedCD {
				t.Errorf("expected Content-Disposition %q, got %q", expectedCD, cd)
			}
		})
	}
}

func TestDownloadFile_BinaryContent(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-dl-binary", "DL Binary")
	admin := createTestAdmin(t)

	binaryContent := string([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0xFF})
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat") {
			return binaryContent, "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-dl-binary", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/download?path=/root/image.png", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	DownloadFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if !bytes.Equal(w.Body.Bytes(), []byte(binaryContent)) {
		t.Errorf("binary content mismatch")
	}
}

func TestDownloadFile_SSHError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-dl-err", "DL Err")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "cat: /root/missing: No such file or directory", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-dl-err", sshClient)
	defer smCleanup()

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/files/download?path=/root/missing", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	DownloadFile(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- CreateNewFile: various content types ---

func TestCreateNewFile_JSONContent(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-create-json", "Create JSON")
	admin := createTestAdmin(t)

	var writtenData string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") {
			data, _ := io.ReadAll(stdin)
			writtenData = string(data)
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-create-json", sshClient)
	defer smCleanup()

	jsonContent := `{"debug":true,"port":8080}`
	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"path":"/root/config.json","content":%q}`, jsonContent)))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if writtenData != jsonContent {
		t.Errorf("expected %q, got %q", jsonContent, writtenData)
	}
}

func TestCreateNewFile_EmptyContent(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-create-empty", "Create Empty")
	admin := createTestAdmin(t)

	var writtenData string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") {
			data, _ := io.ReadAll(stdin)
			writtenData = string(data)
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-create-empty", sshClient)
	defer smCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/empty.txt","content":""}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if writtenData != "" {
		t.Errorf("expected empty content, got %q", writtenData)
	}
}

func TestCreateNewFile_ScriptContent(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-create-script", "Create Script")
	admin := createTestAdmin(t)

	var writtenData string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") {
			data, _ := io.ReadAll(stdin)
			writtenData = string(data)
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-create-script", sshClient)
	defer smCleanup()

	scriptContent := "#!/bin/bash\necho \"Hello $USER\"\n"
	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"path":"/root/run.sh","content":%q}`, scriptContent)))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if writtenData != scriptContent {
		t.Errorf("script content mismatch: expected %q, got %q", scriptContent, writtenData)
	}
}

func TestCreateNewFile_SSHWriteError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-create-fail", "Create Fail")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		io.ReadAll(stdin)
		return "", "No space left on device", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-create-fail", sshClient)
	defer smCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/test.txt","content":"data"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateNewFile(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- CreateDirectory: nested paths ---

func TestCreateDirectory_NestedPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-mkdir-nested", "Mkdir Nested")
	admin := createTestAdmin(t)

	var receivedCmd string
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		receivedCmd = cmd
		return "", "", 0
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-mkdir-nested", sshClient)
	defer smCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/directories", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/root/a/b/c/d/e"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateDirectory(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if !strings.Contains(receivedCmd, "mkdir -p") {
		t.Errorf("expected mkdir -p, got %q", receivedCmd)
	}
	if !strings.Contains(receivedCmd, "/root/a/b/c/d/e") {
		t.Errorf("expected nested path, got %q", receivedCmd)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["path"] != "/root/a/b/c/d/e" {
		t.Errorf("expected path /root/a/b/c/d/e, got %v", resp["path"])
	}
}

func TestCreateDirectory_PermissionDenied(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-mkdir-denied", "Mkdir Denied")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		return "", "mkdir: cannot create directory '/protected/dir': Permission denied", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-mkdir-denied", sshClient)
	defer smCleanup()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/directories", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Body = io.NopCloser(strings.NewReader(`{"path":"/protected/dir"}`))
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	CreateDirectory(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- UploadFile: multipart form upload scenarios ---

func TestUploadFile_BinaryUpload(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-bin", "Upload Bin")
	admin := createTestAdmin(t)

	binaryData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	var receivedData []byte
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") {
			data, _ := io.ReadAll(stdin)
			receivedData = data
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-upload-bin", sshClient)
	defer smCleanup()

	mpBody := &bytes.Buffer{}
	writer := multipart.NewWriter(mpBody)
	part, _ := writer.CreateFormFile("file", "image.png")
	part.Write(binaryData)
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

	if !bytes.Equal(receivedData, binaryData) {
		t.Errorf("binary data mismatch: expected %v, got %v", binaryData, receivedData)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["filename"] != "image.png" {
		t.Errorf("expected filename image.png, got %v", resp["filename"])
	}
	if resp["path"] != "/root/image.png" {
		t.Errorf("expected path /root/image.png, got %v", resp["path"])
	}
}

func TestUploadFile_LargeFile(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-large", "Upload Large")
	admin := createTestAdmin(t)

	// 10MB upload
	largeData := bytes.Repeat([]byte("A"), 10*1024*1024)
	var receivedLen int
	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		if strings.Contains(cmd, "cat >") {
			data, _ := io.ReadAll(stdin)
			receivedLen = len(data)
			return "", "", 0
		}
		return "", "unexpected", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-upload-large", sshClient)
	defer smCleanup()

	mpBody := &bytes.Buffer{}
	writer := multipart.NewWriter(mpBody)
	part, _ := writer.CreateFormFile("file", "large.bin")
	part.Write(largeData)
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
	if receivedLen != len(largeData) {
		t.Errorf("expected %d bytes uploaded, got %d", len(largeData), receivedLen)
	}
}

func TestUploadFile_SSHWriteError(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-fail", "Upload Fail")
	admin := createTestAdmin(t)

	sshClient, sshCleanup := startFileTestSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
		io.ReadAll(stdin)
		return "", "No space left on device", 1
	})
	defer sshCleanup()

	smCleanup := setupSSHManagerWithClient(t, "bot-upload-fail", sshClient)
	defer smCleanup()

	mpBody := &bytes.Buffer{}
	writer := multipart.NewWriter(mpBody)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("upload data"))
	writer.Close()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files/upload?path=/root", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Body = io.NopCloser(mpBody)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestUploadFile_NestedPath(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := createTestInstance(t, "bot-upload-nested", "Upload Nested")
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

	smCleanup := setupSSHManagerWithClient(t, "bot-upload-nested", sshClient)
	defer smCleanup()

	mpBody := &bytes.Buffer{}
	writer := multipart.NewWriter(mpBody)
	part, _ := writer.CreateFormFile("file", "data.csv")
	part.Write([]byte("a,b,c\n1,2,3\n"))
	writer.Close()

	r := newChiRequest("POST", fmt.Sprintf("/api/v1/instances/%d/files/upload?path=/root/data/imports", inst.ID),
		map[string]string{"id": fmt.Sprint(inst.ID)})
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Body = io.NopCloser(mpBody)
	r = middleware.WithUserForTest(r, admin)
	w := httptest.NewRecorder()
	UploadFile(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the path was correctly joined
	if !strings.Contains(writtenPath, "/root/data/imports/data.csv") {
		t.Errorf("expected path /root/data/imports/data.csv, got %q", writtenPath)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["path"] != "/root/data/imports/data.csv" {
		t.Errorf("expected path /root/data/imports/data.csv, got %v", resp["path"])
	}
}
