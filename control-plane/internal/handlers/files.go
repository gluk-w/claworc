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
	"github.com/gluk-w/claworc/control-plane/internal/logutil"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshfiles"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
	"github.com/go-chi/chi/v5"
)

func BrowseFiles(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("No SSH connection for instance: %v", err))
		return
	}

	entries, err := sshfiles.ListDirectory(sshClient, dirPath)
	if err != nil {
		log.Printf("[files] BrowseFiles instance=%s path=%s error duration=%s: %v", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(dirPath), time.Since(start), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list directory: %v", err))
		return
	}

	log.Printf("[files] BrowseFiles instance=%s path=%s entries=%d duration=%s", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(dirPath), len(entries), time.Since(start))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":    dirPath,
		"entries": entries,
	})
}

func ReadFileContent(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("No SSH connection for instance: %v", err))
		return
	}

	content, err := sshfiles.ReadFile(sshClient, filePath)
	if err != nil {
		log.Printf("[files] ReadFileContent instance=%s path=%s error duration=%s: %v", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(filePath), time.Since(start), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to read file: %v", err))
		return
	}

	log.Printf("[files] ReadFileContent instance=%s path=%s bytes=%d duration=%s", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(filePath), len(content), time.Since(start))
	writeJSON(w, http.StatusOK, map[string]string{
		"path":    filePath,
		"content": string(content),
	})
}

func DownloadFile(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("No SSH connection for instance: %v", err))
		return
	}

	content, err := sshfiles.ReadFile(sshClient, filePath)
	if err != nil {
		log.Printf("[files] DownloadFile instance=%s path=%s error duration=%s: %v", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(filePath), time.Since(start), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to download file: %v", err))
		return
	}

	parts := strings.Split(filePath, "/")
	filename := parts[len(parts)-1]

	log.Printf("[files] DownloadFile instance=%s path=%s bytes=%d duration=%s", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(filePath), len(content), time.Since(start))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Write(content)
}

func CreateNewFile(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("No SSH connection for instance: %v", err))
		return
	}

	if err := sshfiles.WriteFile(sshClient, body.Path, []byte(body.Content)); err != nil {
		log.Printf("[files] CreateNewFile instance=%s path=%s error duration=%s: %v", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(body.Path), time.Since(start), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create file: %v", err))
		return
	}

	log.Printf("[files] CreateNewFile instance=%s path=%s bytes=%d duration=%s", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(body.Path), len(body.Content), time.Since(start))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    body.Path,
	})
}

func CreateDirectory(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("No SSH connection for instance: %v", err))
		return
	}

	if err := sshfiles.CreateDirectory(sshClient, body.Path); err != nil {
		log.Printf("[files] CreateDirectory instance=%s path=%s error duration=%s: %v", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(body.Path), time.Since(start), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create directory: %v", err))
		return
	}

	log.Printf("[files] CreateDirectory instance=%s path=%s duration=%s", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(body.Path), time.Since(start))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"path":    body.Path,
	})
}

func UploadFile(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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

	sm := sshtunnel.GetSSHManager()
	if sm == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	sshClient, err := sm.GetClient(inst.Name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("No SSH connection for instance: %v", err))
		return
	}

	if err := sshfiles.WriteFile(sshClient, fullPath, content); err != nil {
		log.Printf("[files] UploadFile instance=%s path=%s error duration=%s: %v", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(fullPath), time.Since(start), err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to upload file: %v", err))
		return
	}

	log.Printf("[files] UploadFile instance=%s path=%s bytes=%d duration=%s", logutil.SanitizeForLog(inst.Name), logutil.SanitizeForLog(fullPath), len(content), time.Since(start))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"path":     fullPath,
		"filename": header.Filename,
	})
}
