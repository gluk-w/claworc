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

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/go-chi/chi/v5"
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

// mountPathTaken reports whether any shared folder other than excludeID already
// uses the given mount path. excludeID = 0 means no exclusion (use for create).
func mountPathTaken(mountPath string, excludeID uint) (bool, error) {
	var count int64
	q := database.DB.Model(&database.SharedFolder{}).Where("mount_path = ?", mountPath)
	if excludeID != 0 {
		q = q.Where("id <> ?", excludeID)
	}
	if err := q.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
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
	taken, err := mountPathTaken(body.MountPath, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to validate mount path")
		return
	}
	if taken {
		writeError(w, http.StatusConflict, "Mount path already in use by another shared folder")
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

	var totalFolders int64
	database.DB.Model(&database.SharedFolder{}).Count(&totalFolders)
	analytics.Track(r.Context(), analytics.EventSharedFolderCreated, map[string]any{
		"total_folders":      totalFolders,
		"agents_shared_with": len(database.ParseSharedFolderInstanceIDs(sf.InstanceIDs)),
	})

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":           sf.ID,
		"name":         sf.Name,
		"mount_path":   sf.MountPath,
		"owner_id":     sf.OwnerID,
		"instance_ids": []uint{},
		"created_at":   sf.CreatedAt.Format("2006-01-02T15:04:05Z"),
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
		"id":           sf.ID,
		"name":         sf.Name,
		"mount_path":   sf.MountPath,
		"owner_id":     sf.OwnerID,
		"instance_ids": database.ParseSharedFolderInstanceIDs(sf.InstanceIDs),
		"created_at":   sf.CreatedAt.Format("2006-01-02T15:04:05Z"),
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
		taken, err := mountPathTaken(*body.MountPath, sf.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to validate mount path")
			return
		}
		if taken {
			writeError(w, http.StatusConflict, "Mount path already in use by another shared folder")
			return
		}
		updates["mount_path"] = *body.MountPath
	}
	oldInstanceIDs := database.ParseSharedFolderInstanceIDs(sf.InstanceIDs)
	newInstanceIDs := oldInstanceIDs
	membershipChanged := false
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
		membershipChanged = true
	}
	mountPathChanged := body.MountPath != nil && *body.MountPath != sf.MountPath

	if len(updates) == 0 {
		writeError(w, http.StatusBadRequest, "No fields to update")
		return
	}

	if err := database.UpdateSharedFolder(sf.ID, updates); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update shared folder")
		return
	}

	for _, target := range computeFolderUpdateRestartTargets(oldInstanceIDs, newInstanceIDs, mountPathChanged, membershipChanged) {
		var inst database.Instance
		if err := database.DB.First(&inst, target.InstanceID).Error; err != nil {
			continue
		}
		restartInstanceAsyncWithToast(inst, callerID(r), target.ToastTitle,
			fmt.Sprintf("%s is being restarted", inst.DisplayName))
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// folderRestartTarget describes one instance that must restart in response to
// a shared-folder update, and the toast title that explains why.
type folderRestartTarget struct {
	InstanceID uint
	ToastTitle string
}

// computeFolderUpdateRestartTargets returns the instances that must restart
// when a shared folder is updated, with the toast title for each.
//
//   - mount-path change affects every instance currently mapped (old ∪ new).
//   - membership-only change affects only added or removed instances
//     (old △ new); instances kept in the set see no change.
//
// Toast title is "Adding shared folder" when the instance is in the new set,
// "Deleting shared folder" when it's only in the old set.
func computeFolderUpdateRestartTargets(oldIDs, newIDs []uint, mountPathChanged, membershipChanged bool) []folderRestartTarget {
	if !mountPathChanged && !membershipChanged {
		return nil
	}
	newSet := map[uint]bool{}
	for _, id := range newIDs {
		newSet[id] = true
	}
	var ids []uint
	if mountPathChanged {
		ids = mergeUintSets(oldIDs, newIDs)
	} else {
		ids = symmetricDiffUint(oldIDs, newIDs)
	}
	out := make([]folderRestartTarget, 0, len(ids))
	for _, id := range ids {
		title := "Adding shared folder"
		if !newSet[id] {
			title = "Deleting shared folder"
		}
		out = append(out, folderRestartTarget{InstanceID: id, ToastTitle: title})
	}
	return out
}

// symmetricDiffUint returns elements present in exactly one of a or b.
func symmetricDiffUint(a, b []uint) []uint {
	inA := map[uint]bool{}
	for _, v := range a {
		inA[v] = true
	}
	inB := map[uint]bool{}
	for _, v := range b {
		inB[v] = true
	}
	result := []uint{}
	for v := range inA {
		if !inB[v] {
			result = append(result, v)
		}
	}
	for v := range inB {
		if !inA[v] {
			result = append(result, v)
		}
	}
	return result
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

	var remaining int64
	database.DB.Model(&database.SharedFolder{}).Count(&remaining)
	analytics.Track(r.Context(), analytics.EventSharedFolderDeleted, map[string]any{
		"remaining_folders": remaining,
	})

	// Auto-restart mapped instances and delete the backing volume
	folderID := sf.ID
	for _, instID := range mappedIDs {
		var inst database.Instance
		if err := database.DB.First(&inst, instID).Error; err != nil {
			continue
		}
		restartInstanceAsyncWithToast(inst, callerID(r), "Deleting shared folder",
			fmt.Sprintf("%s is being restarted", inst.DisplayName))
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
