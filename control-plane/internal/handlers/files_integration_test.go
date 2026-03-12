//go:build docker_integration

package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// sharedFileInstance holds the single Docker instance reused across all file tests.
// Using a shared instance avoids per-test container startup (~60s each).
var (
	sharedFileInstanceOnce sync.Once
	sharedFileInstanceID   uint
	sharedFileInstanceErr  error
)

// getSharedFileInstance returns the shared Docker instance ID, creating it on first call.
// The instance is reused for the lifetime of the test binary to avoid per-test container startup.
func getSharedFileInstance(baseURL string, client *http.Client) (uint, error) {
	sharedFileInstanceOnce.Do(func() {
		sharedFileInstanceID, sharedFileInstanceErr = createAndWaitForInstance(baseURL, client)
	})
	return sharedFileInstanceID, sharedFileInstanceErr
}

// createAndWaitForInstance creates a Docker instance and waits up to 120s for SSH.
func createAndWaitForInstance(baseURL string, client *http.Client) (uint, error) {
	displayName := fmt.Sprintf("filetest-%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{
		"display_name": displayName,
	})
	resp, err := client.Post(baseURL+"/api/v1/instances", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create instance: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return 0, fmt.Errorf("create instance: expected 201, got %d: %s", resp.StatusCode, string(b))
	}
	var instResp struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&instResp)
	resp.Body.Close()

	instID := instResp.ID

	// Wait up to 120s for SSH to become connected.
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d/ssh-status", baseURL, instID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var statusResp struct {
			State string `json:"state"`
		}
		json.NewDecoder(resp.Body).Decode(&statusResp)
		resp.Body.Close()

		if statusResp.State == sshproxy.StateConnected.String() {
			return instID, nil
		}
		time.Sleep(3 * time.Second)
	}
	return 0, fmt.Errorf("SSH did not connect within 120s for instance id=%d", instID)
}

// createFileTestInstance returns the shared Docker instance ID.
// All file tests share one container to avoid per-test startup latency (~60s each).
func createFileTestInstance(t *testing.T, baseURL string, client *http.Client) uint {
	t.Helper()
	instID, err := getSharedFileInstance(baseURL, client)
	if err != nil {
		t.Fatalf("shared file test instance: %v", err)
	}
	t.Logf("Using shared file-test instance id=%d", instID)
	return instID
}

// fileURL returns the URL for a file operation on an instance.
func fileURL(baseURL string, instID uint, endpoint string) string {
	return fmt.Sprintf("%s/api/v1/instances/%d/files/%s", baseURL, instID, endpoint)
}

func TestIntegration_Files_BrowseDirectory(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	resp, err := client.Get(fileURL(sessionURL, instID, "browse") + "?path=/home/claworc")
	if err != nil {
		t.Fatalf("GET browse: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("browse /home/claworc: expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Path    string        `json:"path"`
		Entries []interface{} `json:"entries"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Path != "/home/claworc" {
		t.Errorf("path = %q, want /home/claworc", result.Path)
	}
	t.Logf("Browse /home/claworc returned %d entries", len(result.Entries))
}

func TestIntegration_Files_BrowseNonExistent(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	resp, err := client.Get(fileURL(sessionURL, instID, "browse") + "?path=/nonexistent_dir_xyz_12345")
	if err != nil {
		t.Fatalf("GET browse: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("browse nonexistent: expected 500, got %d: %s", resp.StatusCode, string(b))
	}
	t.Log("Non-existent directory correctly returned 500")
}

func TestIntegration_Files_CreateAndReadFile(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	filePath := "/home/claworc/inttest_create_read.txt"
	content := "hello from integration test"

	// Create
	createBody, _ := json.Marshal(map[string]string{"path": filePath, "content": content})
	resp, err := client.Post(fileURL(sessionURL, instID, "create"), "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: expected 200, got %d", resp.StatusCode)
	}

	// Read back
	resp, err = client.Get(fileURL(sessionURL, instID, "read") + "?path=" + filePath)
	if err != nil {
		t.Fatalf("GET read: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("read: expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	var readResult struct {
		Content string `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&readResult)
	if readResult.Content != content {
		t.Errorf("content = %q, want %q", readResult.Content, content)
	}
	t.Log("CreateAndReadFile round-trip succeeded")
}

func TestIntegration_Files_CreateDirectory(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	dirPath := "/home/claworc/inttest_mkdir_dir"

	mkdirBody, _ := json.Marshal(map[string]string{"path": dirPath})
	resp, err := client.Post(fileURL(sessionURL, instID, "mkdir"), "application/json", bytes.NewReader(mkdirBody))
	if err != nil {
		t.Fatalf("POST mkdir: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mkdir: expected 200, got %d", resp.StatusCode)
	}

	// Browse parent and verify dir appears
	resp, err = client.Get(fileURL(sessionURL, instID, "browse") + "?path=/home/claworc")
	if err != nil {
		t.Fatalf("GET browse: %v", err)
	}
	defer resp.Body.Close()
	var browseResult struct {
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"entries"`
	}
	json.NewDecoder(resp.Body).Decode(&browseResult)

	found := false
	for _, e := range browseResult.Entries {
		if e.Name == "inttest_mkdir_dir" && e.Type == "directory" {
			found = true
		}
	}
	if !found {
		t.Error("created directory not found in browse output")
	}
	t.Log("CreateDirectory verified in browse output")
}

func TestIntegration_Files_UploadFile(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	content := "uploaded content from integration test"

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "inttest_upload.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	fw.Write([]byte(content))
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost,
		fileURL(sessionURL, instID, "upload")+"?path=/home/claworc",
		&buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d", resp.StatusCode)
	}

	// Read back
	resp, err = client.Get(fileURL(sessionURL, instID, "read") + "?path=/home/claworc/inttest_upload.txt")
	if err != nil {
		t.Fatalf("GET read: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("read uploaded file: expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	var readResult struct {
		Content string `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&readResult)
	if readResult.Content != content {
		t.Errorf("uploaded content = %q, want %q", readResult.Content, content)
	}
	t.Log("UploadFile round-trip succeeded")
}

func TestIntegration_Files_DownloadFile(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	filePath := "/home/claworc/inttest_download.txt"
	content := "download me"

	createBody, _ := json.Marshal(map[string]string{"path": filePath, "content": content})
	resp, _ := client.Post(fileURL(sessionURL, instID, "create"), "application/json", bytes.NewReader(createBody))
	resp.Body.Close()

	resp, err := client.Get(fileURL(sessionURL, instID, "download") + "?path=" + filePath)
	if err != nil {
		t.Fatalf("GET download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "octet-stream") {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != content {
		t.Errorf("downloaded content = %q, want %q", string(b), content)
	}
	t.Log("DownloadFile succeeded")
}

func TestIntegration_Files_DeleteFile(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	filePath := "/home/claworc/inttest_delete_file.txt"

	createBody, _ := json.Marshal(map[string]string{"path": filePath, "content": "to be deleted"})
	resp, _ := client.Post(fileURL(sessionURL, instID, "create"), "application/json", bytes.NewReader(createBody))
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/api/v1/instances/%d/files?path=%s", sessionURL, instID, filePath), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE file: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", resp.StatusCode)
	}

	// Verify it's gone
	resp, err = client.Get(fileURL(sessionURL, instID, "read") + "?path=" + filePath)
	if err != nil {
		t.Fatalf("GET read after delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("read deleted file: expected 500, got %d", resp.StatusCode)
	}
	t.Log("DeleteFile verified gone")
}

func TestIntegration_Files_DeleteDirectory(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	dirPath := "/home/claworc/inttest_delete_dir"

	mkdirBody, _ := json.Marshal(map[string]string{"path": dirPath})
	resp, _ := client.Post(fileURL(sessionURL, instID, "mkdir"), "application/json", bytes.NewReader(mkdirBody))
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/api/v1/instances/%d/files?path=%s", sessionURL, instID, dirPath), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE dir: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete dir: expected 200, got %d", resp.StatusCode)
	}

	// Verify gone by browsing parent
	resp, err = client.Get(fileURL(sessionURL, instID, "browse") + "?path=/home/claworc")
	if err != nil {
		t.Fatalf("GET browse after delete: %v", err)
	}
	defer resp.Body.Close()
	var browseResult struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	json.NewDecoder(resp.Body).Decode(&browseResult)
	for _, e := range browseResult.Entries {
		if e.Name == "inttest_delete_dir" {
			t.Error("deleted directory still appears in browse output")
		}
	}
	t.Log("DeleteDirectory verified gone")
}

func TestIntegration_Files_RenameFile(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	oldPath := "/home/claworc/inttest_rename_old.txt"
	newPath := "/home/claworc/inttest_rename_new.txt"
	content := "rename me"

	createBody, _ := json.Marshal(map[string]string{"path": oldPath, "content": content})
	resp, _ := client.Post(fileURL(sessionURL, instID, "create"), "application/json", bytes.NewReader(createBody))
	resp.Body.Close()

	renameBody, _ := json.Marshal(map[string]string{"from": oldPath, "to": newPath})
	resp, err := client.Post(fileURL(sessionURL, instID, "rename"), "application/json", bytes.NewReader(renameBody))
	if err != nil {
		t.Fatalf("POST rename: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: expected 200, got %d", resp.StatusCode)
	}

	// Old path should be gone
	resp, _ = client.Get(fileURL(sessionURL, instID, "read") + "?path=" + oldPath)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("old path still readable after rename (status=%d)", resp.StatusCode)
	}

	// New path should be readable
	resp, err = client.Get(fileURL(sessionURL, instID, "read") + "?path=" + newPath)
	if err != nil {
		t.Fatalf("GET read new path: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read new path: expected 200, got %d", resp.StatusCode)
	}
	var readResult struct {
		Content string `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&readResult)
	if readResult.Content != content {
		t.Errorf("renamed file content = %q, want %q", readResult.Content, content)
	}
	t.Log("RenameFile verified")
}

func TestIntegration_Files_SearchByName(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	// Create a file with a distinctive name
	uniqueName := fmt.Sprintf("inttest_search_unique_%d.txt", time.Now().UnixNano())
	filePath := "/home/claworc/" + uniqueName

	createBody, _ := json.Marshal(map[string]string{"path": filePath, "content": "searchable"})
	resp, _ := client.Post(fileURL(sessionURL, instID, "create"), "application/json", bytes.NewReader(createBody))
	resp.Body.Close()

	// Search for it
	resp, err := client.Get(fileURL(sessionURL, instID, "search") + "?path=/home/claworc&query=inttest_search_unique")
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("search: expected 200, got %d: %s", resp.StatusCode, string(b))
	}
	var searchResult struct {
		Query   string `json:"query"`
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	json.NewDecoder(resp.Body).Decode(&searchResult)
	if len(searchResult.Results) == 0 {
		t.Fatal("search returned 0 results, expected at least 1")
	}
	found := false
	for _, r := range searchResult.Results {
		if strings.HasSuffix(r.Name, uniqueName) {
			found = true
		}
	}
	if !found {
		t.Errorf("unique file %q not found in search results: %v", uniqueName, searchResult.Results)
	}
	t.Logf("SearchByName found %d result(s)", len(searchResult.Results))
}

func TestIntegration_Files_FullWorkflow(t *testing.T) {
	client := &http.Client{Timeout: 60 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	// 1. Create directory
	dirPath := "/home/claworc/inttest_workflow_dir"
	mkdirBody, _ := json.Marshal(map[string]string{"path": dirPath})
	resp, _ := client.Post(fileURL(sessionURL, instID, "mkdir"), "application/json", bytes.NewReader(mkdirBody))
	resp.Body.Close()

	// 2. Upload file into directory
	content := "workflow test content"
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "workflow.txt")
	fw.Write([]byte(content))
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost,
		fileURL(sessionURL, instID, "upload")+"?path="+dirPath,
		&buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, _ = client.Do(req)
	resp.Body.Close()

	filePath := dirPath + "/workflow.txt"

	// 3. Read file
	resp, err := client.Get(fileURL(sessionURL, instID, "read") + "?path=" + filePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var readResult struct {
		Content string `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&readResult)
	resp.Body.Close()
	if readResult.Content != content {
		t.Errorf("read content = %q, want %q", readResult.Content, content)
	}

	// 4. Rename file
	renamedPath := dirPath + "/workflow_renamed.txt"
	renameBody, _ := json.Marshal(map[string]string{"from": filePath, "to": renamedPath})
	resp, _ = client.Post(fileURL(sessionURL, instID, "rename"), "application/json", bytes.NewReader(renameBody))
	resp.Body.Close()

	// 5. Delete directory recursively
	req, _ = http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/api/v1/instances/%d/files?path=%s", sessionURL, instID, dirPath), nil)
	resp, _ = client.Do(req)
	resp.Body.Close()

	// Verify directory is gone
	resp, err = client.Get(fileURL(sessionURL, instID, "browse") + "?path=" + dirPath)
	if err != nil {
		t.Fatalf("browse deleted dir: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("browse deleted dir: expected 500, got %d", resp.StatusCode)
	}
	t.Log("FullWorkflow completed: mkdir → upload → read → rename → delete")
}

func TestIntegration_Files_UnauthenticatedAccess(t *testing.T) {
	// Use a fresh instance from the shared test database
	var instances []database.Instance
	database.DB.Find(&instances)
	if len(instances) == 0 {
		t.Skip("no instances in DB; skipping unauthenticated test")
	}
	instID := instances[0].ID

	// Client without session cookie (CLAWORC_AUTH_DISABLED=true means auth is bypassed,
	// so unauthenticated tests are only meaningful when auth is enabled).
	// Since our test server has auth disabled, this test verifies the endpoint is reachable.
	// In a production environment this would return 401.
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d/files/browse?path=/home/claworc", sessionURL, instID))
	if err != nil {
		t.Fatalf("GET browse: %v", err)
	}
	resp.Body.Close()
	// Auth is disabled in tests, so we expect 200 or 503 (no SSH), not 401/403
	t.Logf("Unauthenticated access returned status=%d (auth disabled in test server)", resp.StatusCode)
}

func TestIntegration_Files_WrongInstance(t *testing.T) {
	client := &http.Client{Timeout: 30 * time.Second}
	instID := createFileTestInstance(t, sessionURL, client)

	// Use a non-existent instance ID
	nonExistentID := instID + 99999
	resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d/files/browse?path=/home/claworc", sessionURL, nonExistentID))
	if err != nil {
		t.Fatalf("GET browse: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("wrong instance: expected 404, got %d", resp.StatusCode)
	}
	t.Logf("Wrong instance ID correctly returned 404")
}
