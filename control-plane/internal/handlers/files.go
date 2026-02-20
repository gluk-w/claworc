package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/tunnel"
	"github.com/go-chi/chi/v5"
)

// tunnelFilesRequest mirrors the agent-side files protocol request.
type tunnelFilesRequest struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

// tunnelFilesResponse mirrors the agent-side files protocol response.
type tunnelFilesResponse struct {
	Entries []json.RawMessage `json:"entries,omitempty"`
	Content string            `json:"content,omitempty"`
	OK      bool              `json:"ok,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// tunnelFileOp opens a tunnel "files" stream, sends a JSON request, and
// decodes the JSON response. The caller is responsible for interpreting
// the response fields.
func tunnelFileOp(ctx context.Context, tc *tunnel.TunnelClient, req tunnelFilesRequest) (*tunnelFilesResponse, error) {
	stream, err := tc.OpenChannel(ctx, tunnel.ChannelFiles)
	if err != nil {
		return nil, fmt.Errorf("open files channel: %w", err)
	}
	defer stream.Close()

	if err := json.NewEncoder(stream).Encode(req); err != nil {
		return nil, fmt.Errorf("encode files request: %w", err)
	}

	// Signal that we are done writing so the agent's decoder sees EOF cleanly.
	if hw, ok := stream.(halfWriter); ok {
		hw.CloseWrite()
	}

	var resp tunnelFilesResponse
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode files response: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	return &resp, nil
}

// halfWriter is implemented by yamux streams to allow half-closing the write side.
type halfWriter interface {
	CloseWrite() error
}

func BrowseFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/root"
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	tc := tunnel.Manager.Get(inst.ID)
	if tc == nil {
		writeError(w, http.StatusServiceUnavailable, "No tunnel available")
		return
	}

	resp, err := tunnelFileOp(r.Context(), tc, tunnelFilesRequest{Op: "browse", Path: dirPath})
	if err != nil {
		log.Printf("Failed to list directory %s for instance %s: %v", dirPath, inst.Name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list directory: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":    dirPath,
		"entries": resp.Entries,
	})
}

func ReadFileContent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "path parameter required")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	tc := tunnel.Manager.Get(inst.ID)
	if tc == nil {
		writeError(w, http.StatusServiceUnavailable, "No tunnel available")
		return
	}

	resp, err := tunnelFileOp(r.Context(), tc, tunnelFilesRequest{Op: "read", Path: filePath})
	if err != nil {
		log.Printf("Failed to read file %s for instance %s: %v", filePath, inst.Name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read file: %v", err))
		return
	}

	content, err := base64.StdEncoding.DecodeString(resp.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode file content")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"path":    filePath,
		"content": string(content),
	})
}

func DownloadFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "path parameter required")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	tc := tunnel.Manager.Get(inst.ID)
	if tc == nil {
		writeError(w, http.StatusServiceUnavailable, "No tunnel available")
		return
	}

	resp, err := tunnelFileOp(r.Context(), tc, tunnelFilesRequest{Op: "read", Path: filePath})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to download file: %v", err))
		return
	}

	content, err := base64.StdEncoding.DecodeString(resp.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode file content")
		return
	}

	parts := strings.Split(filePath, "/")
	filename := parts[len(parts)-1]

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Write(content)
}

func CreateNewFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	tc := tunnel.Manager.Get(inst.ID)
	if tc == nil {
		writeError(w, http.StatusServiceUnavailable, "No tunnel available")
		return
	}

	b64Content := base64.StdEncoding.EncodeToString([]byte(body.Content))
	if _, err := tunnelFileOp(r.Context(), tc, tunnelFilesRequest{Op: "create", Path: body.Path, Content: b64Content}); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create file: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    body.Path,
	})
}

func CreateDirectory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	tc := tunnel.Manager.Get(inst.ID)
	if tc == nil {
		writeError(w, http.StatusServiceUnavailable, "No tunnel available")
		return
	}

	if _, err := tunnelFileOp(r.Context(), tc, tunnelFilesRequest{Op: "mkdir", Path: body.Path}); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create directory: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    body.Path,
	})
}

func UploadFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		writeError(w, http.StatusBadRequest, "path parameter required")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field required")
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read upload")
		return
	}

	fullPath := path.Join(dirPath, header.Filename)
	if strings.HasSuffix(dirPath, header.Filename) {
		fullPath = dirPath
	}

	tc := tunnel.Manager.Get(inst.ID)
	if tc == nil {
		writeError(w, http.StatusServiceUnavailable, "No tunnel available")
		return
	}

	b64Content := base64.StdEncoding.EncodeToString(content)
	if _, err := tunnelFileOp(r.Context(), tc, tunnelFilesRequest{Op: "write", Path: fullPath, Content: b64Content}); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to upload file: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"path":     fullPath,
		"filename": header.Filename,
	})
}

