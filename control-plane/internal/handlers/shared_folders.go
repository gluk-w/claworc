package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/config"
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

// ValidateHostMountPath verifies that a host bind-mount source path is permitted
// by the operator allowlist. It is the single security gate for host-backed
// shared folders. allowed is the list of permitted host path prefixes; an empty
// list means the feature is disabled and every path is rejected.
func ValidateHostMountPath(hostPath string, allowed []string) error {
	// 1. Feature gate.
	hasPrefix := false
	for _, p := range allowed {
		if strings.TrimSpace(p) != "" {
			hasPrefix = true
			break
		}
	}
	if !hasPrefix {
		return fmt.Errorf("host bind mounts are not enabled")
	}

	// 2. Absolute and already-clean (rejects "..", "//", trailing slashes).
	if !strings.HasPrefix(hostPath, "/") {
		return fmt.Errorf("host path must be absolute")
	}
	if filepath.Clean(hostPath) != hostPath {
		return fmt.Errorf("host path must be a clean, absolute path (no '..', '.', or trailing slashes)")
	}
	if strings.Contains(hostPath, "..") {
		return fmt.Errorf("host path must not contain '..'")
	}

	// 3. Allowlist containment (cleaned-vs-cleaned).
	if !pathWithinAllowlist(hostPath, allowed) {
		return fmt.Errorf("host path is not within an allowed prefix")
	}

	// 4. Symlink hardening: resolve and re-check. EvalSymlinks fails if the path
	//    does not exist on the control-plane host (e.g. Kubernetes node paths);
	//    in that case we fall back to the lexical check already performed above.
	//    The allowlist prefixes are resolved too so that a symlinked ancestor of
	//    the prefix itself (e.g. macOS /var -> /private/var) does not cause a
	//    false rejection — only an escape beyond the resolved prefix is blocked.
	if resolved, err := filepath.EvalSymlinks(hostPath); err == nil {
		if !pathWithinAllowlist(resolved, resolveAllowlist(allowed)) {
			return fmt.Errorf("host path resolves (via symlink) outside the allowed prefixes")
		}
	}

	// 5. Defense-in-depth denylist of sensitive paths, even if the allowlist
	//    would otherwise permit them.
	for _, denied := range hostMountDenylist() {
		if hostPath == denied || strings.HasPrefix(hostPath, denied+"/") {
			return fmt.Errorf("host path overlaps a protected location")
		}
	}

	return nil
}

// pathWithinAllowlist reports whether p equals or is nested under any non-empty
// allowlisted prefix. Both sides are cleaned before comparison.
func pathWithinAllowlist(p string, allowed []string) bool {
	cp := filepath.Clean(p)
	for _, prefix := range allowed {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		prefix = filepath.Clean(prefix)
		if cp == prefix || strings.HasPrefix(cp, prefix+"/") {
			return true
		}
	}
	return false
}

// ensureHostPathDir creates the host bind-mount source directory (recursively)
// if it does not yet exist. It only applies to the Docker backend, where the
// control plane shares the host filesystem; on Kubernetes the path lives on the
// node and is created/managed there. The caller must have already validated the
// path against the allowlist. Returns ok=false with a user-facing message if the
// directory cannot be created or is occupied by a non-directory.
func ensureHostPathDir(hostPath string) (ok bool, msg string) {
	orch := orchestrator.Get()
	if orch == nil || orch.BackendName() != "docker" {
		return true, ""
	}
	if info, err := os.Stat(hostPath); err == nil {
		if !info.IsDir() {
			return false, "host path exists but is not a directory"
		}
		return true, ""
	} else if !os.IsNotExist(err) {
		return false, "host path is not accessible"
	}
	if err := os.MkdirAll(hostPath, 0o755); err != nil {
		return false, "could not create host directory"
	}
	return true, ""
}

// resolveAllowlist returns the allowlist prefixes with symlinks resolved where
// possible, so the resolved-path containment check compares like with like.
func resolveAllowlist(allowed []string) []string {
	out := make([]string, 0, len(allowed))
	for _, prefix := range allowed {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(prefix); err == nil {
			out = append(out, resolved)
		} else {
			out = append(out, prefix)
		}
	}
	return out
}

// hostMountDenylist returns paths that must never be bind-mounted regardless of
// the allowlist: the Claworc data directory (Fernet key + encrypted secrets),
// the Docker socket, and the SSH key directory.
func hostMountDenylist() []string {
	denied := []string{
		"/var/run/docker.sock",
		"/run/docker.sock",
	}
	if dp := strings.TrimSpace(config.Cfg.DataPath); dp != "" {
		denied = append(denied, filepath.Clean(dp))
	}
	return denied
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
		HostPath    string `json:"host_path"`
		ReadOnly    bool   `json:"read_only"`
		OwnerID     uint   `json:"owner_id"`
		InstanceIDs []uint `json:"instance_ids"`
		TeamIDs     []uint `json:"team_ids"`
		CreatedAt   string `json:"created_at"`
	}

	result := make([]folderResponse, 0, len(folders))
	for _, sf := range folders {
		result = append(result, folderResponse{
			ID:          sf.ID,
			Name:        sf.Name,
			MountPath:   sf.MountPath,
			HostPath:    sf.HostPath,
			ReadOnly:    sf.ReadOnly,
			OwnerID:     sf.OwnerID,
			InstanceIDs: database.ParseSharedFolderInstanceIDs(sf.InstanceIDs),
			TeamIDs:     database.ParseTeamIDs(sf.TeamIDs),
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
		HostPath  string `json:"host_path"`
		ReadOnly  *bool  `json:"read_only"`
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

	// Host-backed folders default to read-only.
	readOnly := true
	if body.HostPath != "" {
		if err := ValidateHostMountPath(body.HostPath, config.Cfg.AllowedHostMounts); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid host path: %v", err))
			return
		}
		if ok, msg := ensureHostPathDir(body.HostPath); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid host path: %s", msg))
			return
		}
		if body.ReadOnly != nil {
			readOnly = *body.ReadOnly
		}
	}

	sf := &database.SharedFolder{
		Name:      body.Name,
		MountPath: body.MountPath,
		OwnerID:   user.ID,
		HostPath:  body.HostPath,
		ReadOnly:  readOnly,
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
		"host_path":    sf.HostPath,
		"read_only":    sf.ReadOnly,
		"owner_id":     sf.OwnerID,
		"instance_ids": []uint{},
		"team_ids":     []uint{},
		"created_at":   sf.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// HostMountConfig reports whether host-backed shared folders are enabled and,
// if so, the allowlisted host path prefixes. The frontend uses this to decide
// whether to show the "Mount to Host" option and to hint allowed locations.
func HostMountConfig(w http.ResponseWriter, r *http.Request) {
	if middleware.GetUser(r) == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	prefixes := []string{}
	for _, p := range config.Cfg.AllowedHostMounts {
		if p = strings.TrimSpace(p); p != "" {
			prefixes = append(prefixes, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":          len(prefixes) > 0,
		"allowed_prefixes": prefixes,
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
		"host_path":    sf.HostPath,
		"read_only":    sf.ReadOnly,
		"owner_id":     sf.OwnerID,
		"instance_ids": database.ParseSharedFolderInstanceIDs(sf.InstanceIDs),
		"team_ids":     database.ParseTeamIDs(sf.TeamIDs),
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
		HostPath    *string `json:"host_path"`
		ReadOnly    *bool   `json:"read_only"`
		InstanceIDs *[]uint `json:"instance_ids"`
		TeamIDs     *[]uint `json:"team_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Host path is immutable after creation.
	if body.HostPath != nil && *body.HostPath != sf.HostPath {
		writeError(w, http.StatusBadRequest, "Host path cannot be changed after creation")
		return
	}

	updates := map[string]interface{}{}
	if body.Name != nil && *body.Name != "" {
		updates["name"] = *body.Name
	}
	readOnlyChanged := false
	if body.ReadOnly != nil && *body.ReadOnly != sf.ReadOnly {
		// Read-only mode only affects host-backed folders.
		if sf.HostPath == "" {
			writeError(w, http.StatusBadRequest, "Read-only mode only applies to host-backed shared folders")
			return
		}
		updates["read_only"] = *body.ReadOnly
		readOnlyChanged = true
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
	oldTeamIDs := database.ParseTeamIDs(sf.TeamIDs)
	newInstanceIDs := oldInstanceIDs
	newTeamIDs := oldTeamIDs
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
	if body.TeamIDs != nil {
		// Validate caller can manage each team being attached.
		for _, tid := range *body.TeamIDs {
			if !middleware.CanManageTeam(r, tid) {
				writeError(w, http.StatusForbidden, fmt.Sprintf("Not authorized to attach team %d", tid))
				return
			}
		}
		newTeamIDs = *body.TeamIDs
		updates["team_ids"] = database.EncodeTeamIDs(newTeamIDs)
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

	// Restart targets are computed from the *effective* instance set (explicit
	// IDs unioned with instances belonging to a covered team) so a new team
	// triggers restarts for all of its current instances.
	oldEffective := expandFolderEffectiveInstances(oldInstanceIDs, oldTeamIDs)
	newEffective := expandFolderEffectiveInstances(newInstanceIDs, newTeamIDs)

	for _, target := range computeFolderUpdateRestartTargets(oldEffective, newEffective, mountPathChanged || readOnlyChanged, membershipChanged) {
		var inst database.Instance
		if err := database.DB.First(&inst, target.InstanceID).Error; err != nil {
			continue
		}
		restartInstanceAsyncWithToast(inst, callerID(r), target.ToastTitle,
			fmt.Sprintf("%s is being restarted", inst.DisplayName))
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// expandFolderEffectiveInstances returns the de-duplicated list of instance IDs
// a folder covers, given its explicit InstanceIDs and TeamIDs columns. Used to
// compute restart targets in UpdateSharedFolder so a team-level change kicks
// every team-member instance into a restart.
func expandFolderEffectiveInstances(instanceIDs, teamIDs []uint) []uint {
	seen := map[uint]struct{}{}
	out := []uint{}
	for _, id := range instanceIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(teamIDs) > 0 {
		var rows []database.Instance
		if err := database.DB.
			Select("id").
			Where("team_id IN ?", teamIDs).
			Find(&rows).Error; err == nil {
			for _, r := range rows {
				if _, ok := seen[r.ID]; ok {
					continue
				}
				seen[r.ID] = struct{}{}
				out = append(out, r.ID)
			}
		}
	}
	return out
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
