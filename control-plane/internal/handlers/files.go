package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
)

func BrowseFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/home/claworc"
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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	entries, err := sshproxy.ListDirectory(client, dirPath)
	if err != nil {
		log.Printf("Failed to list directory %s for instance %s: %v", utils.SanitizeForLog(dirPath), utils.SanitizeForLog(inst.Name), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list directory: %v", err))
		return
	}
	log.Printf("[files] BrowseFiles instance=%d path=%s entries=%d duration=%s", inst.ID, utils.SanitizeForLog(dirPath), len(entries), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=browse, path=%s, entries=%d", dirPath, len(entries)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":    dirPath,
		"entries": entries,
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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	content, err := sshproxy.ReadFile(client, filePath)
	if err != nil {
		log.Printf("Failed to read file %s for instance %s: %v", utils.SanitizeForLog(filePath), utils.SanitizeForLog(inst.Name), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read file: %v", err))
		return
	}
	log.Printf("[files] ReadFileContent instance=%d path=%s size=%d duration=%s", inst.ID, utils.SanitizeForLog(filePath), len(content), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=read, path=%s, size=%d", filePath, len(content)))

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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	content, err := sshproxy.ReadFile(client, filePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to download file: %v", err))
		return
	}
	log.Printf("[files] DownloadFile instance=%d path=%s size=%d duration=%s", inst.ID, utils.SanitizeForLog(filePath), len(content), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=download, path=%s, size=%d", filePath, len(content)))

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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	if err := sshproxy.WriteFile(client, body.Path, []byte(body.Content)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create file: %v", err))
		return
	}
	log.Printf("[files] CreateNewFile instance=%d path=%s size=%d duration=%s", inst.ID, utils.SanitizeForLog(body.Path), len(body.Content), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=create, path=%s, size=%d", body.Path, len(body.Content)))

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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	if err := sshproxy.CreateDirectory(client, body.Path); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create directory: %v", err))
		return
	}
	log.Printf("[files] CreateDirectory instance=%d path=%s duration=%s", inst.ID, utils.SanitizeForLog(body.Path), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=mkdir, path=%s", body.Path))

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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	if err := sshproxy.WriteFile(client, fullPath, content); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to upload file: %v", err))
		return
	}
	log.Printf("[files] UploadFile instance=%d path=%s size=%d duration=%s", inst.ID, utils.SanitizeForLog(fullPath), len(content), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=upload, path=%s, size=%d", fullPath, len(content)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"path":     fullPath,
		"filename": header.Filename,
	})
}

func DeleteFile(w http.ResponseWriter, r *http.Request) {
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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	if err := sshproxy.DeletePath(client, filePath); err != nil {
		log.Printf("Failed to delete %s for instance %s: %v", utils.SanitizeForLog(filePath), utils.SanitizeForLog(inst.Name), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete: %v", err))
		return
	}
	log.Printf("[files] DeleteFile instance=%d path=%s duration=%s", inst.ID, utils.SanitizeForLog(filePath), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=delete, path=%s", filePath))

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func RenameFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.From == "" || body.To == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	if err := sshproxy.RenamePath(client, body.From, body.To); err != nil {
		log.Printf("Failed to rename %s -> %s for instance %s: %v", utils.SanitizeForLog(body.From), utils.SanitizeForLog(body.To), utils.SanitizeForLog(inst.Name), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to rename: %v", err))
		return
	}
	log.Printf("[files] RenameFile instance=%d from=%s to=%s duration=%s", inst.ID, utils.SanitizeForLog(body.From), utils.SanitizeForLog(body.To), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=rename, from=%s, to=%s", body.From, body.To))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    body.To,
	})
}

func SearchFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/home/claworc"
	}
	query := r.URL.Query().Get("query")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter required")
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

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	client, ok := SSHMgr.GetConnection(inst.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "No SSH connection for instance")
		return
	}

	start := time.Now()
	results, err := sshproxy.SearchFiles(client, dirPath, query)
	if err != nil {
		log.Printf("Failed to search files in %s for instance %s: %v", utils.SanitizeForLog(dirPath), utils.SanitizeForLog(inst.Name), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to search: %v", err))
		return
	}
	log.Printf("[files] SearchFiles instance=%d path=%s query=%s results=%d duration=%s", inst.ID, utils.SanitizeForLog(dirPath), utils.SanitizeForLog(query), len(results), time.Since(start))
	auditFileOp(r, inst.ID, fmt.Sprintf("op=search, path=%s, query=%s, results=%d", dirPath, query, len(results)))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":    dirPath,
		"query":   query,
		"results": results,
	})
}

// auditFileOp logs a file operation to the audit log if it is initialized.
func auditFileOp(r *http.Request, instanceID uint, details string) {
	if AuditLog == nil {
		return
	}
	user := middleware.GetUser(r)
	username := "unknown"
	if user != nil {
		username = user.Username
	}
	AuditLog.LogFileOperation(instanceID, username, details)
}

// getUsername extracts the username from the request context.
func getUsername(r *http.Request) string {
	user := middleware.GetUser(r)
	if user != nil {
		return user.Username
	}
	return "unknown"
}

// auditLog is a convenience wrapper that checks if AuditLog is initialized.
func auditLog(eventType sshaudit.EventType, instanceID uint, user, details string) {
	if AuditLog != nil {
		AuditLog.Log(eventType, instanceID, user, details)
	}
}
