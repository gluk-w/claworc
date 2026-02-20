package services

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
)

// File operation request/response types for the files channel protocol.
// Each yamux stream carries exactly one JSON request and one JSON response.

type filesRequest struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"` // base64-encoded for write/create
}

type filesResponse struct {
	Entries []fileEntry `json:"entries,omitempty"`
	Content string      `json:"content,omitempty"` // base64-encoded for read
	OK      bool        `json:"ok,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type fileEntry struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Size        *string `json:"size"`
	Permissions string  `json:"permissions"`
}

// HandleFilesStream handles a single file operation over a yamux stream.
// It reads one JSON request, executes the operation, and writes one JSON response.
func HandleFilesStream(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req filesRequest
	if err := dec.Decode(&req); err != nil {
		log.Printf("files: failed to decode request: %v", err)
		enc.Encode(filesResponse{Error: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	// Sanitize the path to prevent directory traversal.
	cleanPath := filepath.Clean(req.Path)

	switch req.Op {
	case "browse":
		handleBrowse(enc, cleanPath)
	case "read":
		handleRead(enc, cleanPath)
	case "write":
		handleWrite(enc, cleanPath, req.Content)
	case "create":
		handleCreate(enc, cleanPath, req.Content)
	case "mkdir":
		handleMkdir(enc, cleanPath)
	default:
		enc.Encode(filesResponse{Error: fmt.Sprintf("unknown op: %s", req.Op)})
	}
}

func handleBrowse(enc *json.Encoder, dirPath string) {
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		enc.Encode(filesResponse{Error: fmt.Sprintf("failed to read directory: %v", err)})
		return
	}

	entries := make([]fileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		entry := fileEntry{
			Name:        de.Name(),
			Permissions: formatPermissions(de),
		}

		if de.IsDir() {
			entry.Type = "dir"
		} else if de.Type()&fs.ModeSymlink != 0 {
			entry.Type = "link"
		} else {
			entry.Type = "file"
		}

		if info, err := de.Info(); err == nil && !de.IsDir() {
			size := fmt.Sprintf("%d", info.Size())
			entry.Size = &size
		}

		entries = append(entries, entry)
	}

	enc.Encode(filesResponse{Entries: entries})
}

func handleRead(enc *json.Encoder, filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		enc.Encode(filesResponse{Error: fmt.Sprintf("failed to read file: %v", err)})
		return
	}

	enc.Encode(filesResponse{Content: base64.StdEncoding.EncodeToString(data)})
}

func handleWrite(enc *json.Encoder, filePath string, content string) {
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		enc.Encode(filesResponse{Error: fmt.Sprintf("invalid base64 content: %v", err)})
		return
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		enc.Encode(filesResponse{Error: fmt.Sprintf("failed to write file: %v", err)})
		return
	}

	enc.Encode(filesResponse{OK: true})
}

func handleCreate(enc *json.Encoder, filePath string, content string) {
	var data []byte
	if content != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(content)
		if err != nil {
			enc.Encode(filesResponse{Error: fmt.Sprintf("invalid base64 content: %v", err)})
			return
		}
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		enc.Encode(filesResponse{Error: fmt.Sprintf("failed to create file: %v", err)})
		return
	}

	enc.Encode(filesResponse{OK: true})
}

func handleMkdir(enc *json.Encoder, dirPath string) {
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		enc.Encode(filesResponse{Error: fmt.Sprintf("failed to create directory: %v", err)})
		return
	}

	enc.Encode(filesResponse{OK: true})
}

func formatPermissions(de fs.DirEntry) string {
	info, err := de.Info()
	if err != nil {
		return "----------"
	}
	return info.Mode().String()
}
