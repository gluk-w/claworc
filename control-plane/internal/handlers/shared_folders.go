package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// reservedMountPrefixes are paths that must not be used as shared folder mount paths.
var reservedMountPrefixes = []string{
	"/home/claworc",
	"/home/linuxbrew",
	"/dev/shm",
}

func isValidMountPath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	for _, prefix := range reservedMountPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return false
		}
	}
	return true
}

func ListSharedFolders(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	folders, err := database.ListSharedFolders(user.ID, user.Role == "admin")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list shared folders")
		return
	}

	type folderResponse struct {
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		MountPath   string `json:"mount_path"`
		OwnerID     uint   `json:"owner_id"`
		InstanceIDs []uint `json:"instance_ids"`
		CreatedAt   string `json:"created_at"`
	}

	result := make([]folderResponse, 0, len(folders))
	for _, sf := range folders {
		result = append(result, folderResponse{
			ID:          sf.ID,
			Name:        sf.Name,
			MountPath:   sf.MountPath,
			OwnerID:     sf.OwnerID,
			InstanceIDs: database.ParseSharedFolderInstanceIDs(sf.InstanceIDs),
			CreatedAt:   sf.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func CreateSharedFolder(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var body struct {
		Name      string `json:"name"`
		MountPath string `json:"mount_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if !isValidMountPath(body.MountPath) {
		writeError(w, http.StatusBadRequest, "Invalid mount path: must be absolute and not conflict with system paths")
		return
	}

	sf := &database.SharedFolder{
		Name:      body.Name,
		MountPath: body.MountPath,
		OwnerID:   user.ID,
	}
	if err := database.CreateSharedFolder(sf); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create shared folder")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":          sf.ID,
		"name":        sf.Name,
		"mount_path":  sf.MountPath,
		"owner_id":    sf.OwnerID,
		"instance_ids": []uint{},
		"created_at":  sf.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func GetSharedFolder(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid folder ID")
		return
	}

	sf, err := database.GetSharedFolder(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Shared folder not found")
		return
	}

	if user.Role != "admin" && sf.OwnerID != user.ID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":          sf.ID,
		"name":        sf.Name,
		"mount_path":  sf.MountPath,
		"owner_id":    sf.OwnerID,
		"instance_ids": database.ParseSharedFolderInstanceIDs(sf.InstanceIDs),
		"created_at":  sf.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func UpdateSharedFolder(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid folder ID")
		return
	}

	sf, err := database.GetSharedFolder(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Shared folder not found")
		return
	}

	if user.Role != "admin" && sf.OwnerID != user.ID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	var body struct {
		Name        *string `json:"name"`
		MountPath   *string `json:"mount_path"`
		InstanceIDs *[]uint `json:"instance_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	updates := map[string]interface{}{}
	if body.Name != nil && *body.Name != "" {
		updates["name"] = *body.Name
	}
	if body.MountPath != nil {
		if !isValidMountPath(*body.MountPath) {
			writeError(w, http.StatusBadRequest, "Invalid mount path: must be absolute and not conflict with system paths")
			return
		}
		updates["mount_path"] = *body.MountPath
	}
	var newInstanceIDs []uint
	mountChanged := false
	if body.InstanceIDs != nil {
		// Validate user has access to all specified instances
		for _, instID := range *body.InstanceIDs {
			if !middleware.CanAccessInstance(r, instID) {
				writeError(w, http.StatusForbidden, fmt.Sprintf("Access denied to instance %d", instID))
				return
			}
		}
		newInstanceIDs = *body.InstanceIDs
		updates["instance_ids"] = database.EncodeSharedFolderInstanceIDs(newInstanceIDs)
		mountChanged = true
	}
	if body.MountPath != nil {
		mountChanged = true
	}

	if len(updates) == 0 {
		writeError(w, http.StatusBadRequest, "No fields to update")
		return
	}

	oldInstanceIDs := database.ParseSharedFolderInstanceIDs(sf.InstanceIDs)

	if err := database.UpdateSharedFolder(sf.ID, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update shared folder")
		return
	}

	// Auto-restart affected instances so mount changes take effect immediately
	if mountChanged {
		affectedIDs := mergeUintSets(oldInstanceIDs, newInstanceIDs)
		for _, instID := range affectedIDs {
			var inst database.Instance
			if err := database.DB.First(&inst, instID).Error; err != nil {
				continue
			}
			restartInstanceAsync(inst)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// mergeUintSets returns the union of two uint slices with no duplicates.
func mergeUintSets(a, b []uint) []uint {
	seen := map[uint]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		seen[v] = true
	}
	result := make([]uint, 0, len(seen))
	for v := range seen {
		result = append(result, v)
	}
	return result
}

func DeleteSharedFolder(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid folder ID")
		return
	}

	sf, err := database.GetSharedFolder(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Shared folder not found")
		return
	}

	if user.Role != "admin" && sf.OwnerID != user.ID {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	mappedIDs := database.ParseSharedFolderInstanceIDs(sf.InstanceIDs)

	if err := database.DeleteSharedFolder(sf.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete shared folder")
		return
	}

	// Auto-restart mapped instances and delete the backing volume
	folderID := sf.ID
	for _, instID := range mappedIDs {
		var inst database.Instance
		if err := database.DB.First(&inst, instID).Error; err != nil {
			continue
		}
		restartInstanceAsync(inst)
	}

	// Delete the backing volume in the background (after instances have unmounted it)
	if orch := orchestrator.Get(); orch != nil {
		go func() {
			// Allow time for instances to restart and release the volume
			time.Sleep(10 * time.Second)
			if err := orch.DeleteSharedVolume(context.Background(), folderID); err != nil {
				log.Printf("Failed to delete shared volume for folder %d: %v", folderID, err)
			} else {
				log.Printf("Deleted shared volume for folder %d", folderID)
			}
		}()
	}

	w.WriteHeader(http.StatusNoContent)
}

