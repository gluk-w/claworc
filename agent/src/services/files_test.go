package services

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleFilesStream_Browse(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	c1, c2 := net.Pipe()
	done := make(chan struct{})

	go func() {
		defer close(done)
		HandleFilesStream(c2)
	}()

	// Send request. The handler reads one JSON object, not until EOF.
	req := filesRequest{Op: "browse", Path: dir}
	json.NewEncoder(c1).Encode(req)

	var resp filesResponse
	if err := json.NewDecoder(c1).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	c1.Close()
	<-done

	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}

	// Verify entries contain our file and dir.
	names := make(map[string]bool)
	for _, e := range resp.Entries {
		names[e.Name] = true
	}
	if !names["hello.txt"] {
		t.Error("missing hello.txt in entries")
	}
	if !names["subdir"] {
		t.Error("missing subdir in entries")
	}
}

func TestHandleFilesStream_ReadWrite(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	originalContent := "hello world"
	os.WriteFile(filePath, []byte(originalContent), 0644)

	// Test read.
	c1, c2 := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleFilesStream(c2)
	}()

	req := filesRequest{Op: "read", Path: filePath}
	json.NewEncoder(c1).Encode(req)

	var resp filesResponse
	json.NewDecoder(c1).Decode(&resp)
	c1.Close()
	<-done

	if resp.Error != "" {
		t.Fatalf("read error: %s", resp.Error)
	}

	decoded, err := base64.StdEncoding.DecodeString(resp.Content)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != originalContent {
		t.Fatalf("expected %q, got %q", originalContent, string(decoded))
	}

	// Test write.
	newContent := "updated content"
	c1, c2 = net.Pipe()
	done = make(chan struct{})
	go func() {
		defer close(done)
		HandleFilesStream(c2)
	}()

	writeReq := filesRequest{
		Op:      "write",
		Path:    filePath,
		Content: base64.StdEncoding.EncodeToString([]byte(newContent)),
	}
	json.NewEncoder(c1).Encode(writeReq)

	var writeResp filesResponse
	json.NewDecoder(c1).Decode(&writeResp)
	c1.Close()
	<-done

	if writeResp.Error != "" {
		t.Fatalf("write error: %s", writeResp.Error)
	}
	if !writeResp.OK {
		t.Fatal("expected OK to be true")
	}

	// Verify the file was updated.
	data, _ := os.ReadFile(filePath)
	if string(data) != newContent {
		t.Fatalf("file content mismatch: expected %q, got %q", newContent, string(data))
	}
}

func TestHandleFilesStream_Mkdir(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, "a", "b", "c")

	c1, c2 := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleFilesStream(c2)
	}()

	req := filesRequest{Op: "mkdir", Path: newDir}
	json.NewEncoder(c1).Encode(req)

	var resp filesResponse
	json.NewDecoder(c1).Decode(&resp)
	c1.Close()
	<-done

	if resp.Error != "" {
		t.Fatalf("mkdir error: %s", resp.Error)
	}
	if !resp.OK {
		t.Fatal("expected OK to be true")
	}

	info, err := os.Stat(newDir)
	if err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestHandleFilesStream_Create(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "new.txt")

	c1, c2 := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleFilesStream(c2)
	}()

	content := "initial content"
	req := filesRequest{
		Op:      "create",
		Path:    filePath,
		Content: base64.StdEncoding.EncodeToString([]byte(content)),
	}
	json.NewEncoder(c1).Encode(req)

	var resp filesResponse
	json.NewDecoder(c1).Decode(&resp)
	c1.Close()
	<-done

	if resp.Error != "" {
		t.Fatalf("create error: %s", resp.Error)
	}
	if !resp.OK {
		t.Fatal("expected OK to be true")
	}

	data, _ := os.ReadFile(filePath)
	if string(data) != content {
		t.Fatalf("file content mismatch: expected %q, got %q", content, string(data))
	}
}

func TestHandleFilesStream_UnknownOp(t *testing.T) {
	c1, c2 := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleFilesStream(c2)
	}()

	req := filesRequest{Op: "delete", Path: "/tmp/foo"}
	json.NewEncoder(c1).Encode(req)

	var resp filesResponse
	json.NewDecoder(c1).Decode(&resp)
	c1.Close()
	<-done

	if resp.Error == "" {
		t.Fatal("expected error for unknown op")
	}
}
