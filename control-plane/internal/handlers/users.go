package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

func ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := database.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list users")
		return
	}

	type userResponse struct {
		ID                 uint   `json:"id"`
		Username           string `json:"username"`
		Role               string `json:"role"`
		CanCreateInstances bool   `json:"can_create_instances"`
		LastLoginAt        string `json:"last_login_at"`
		CreatedAt          string `json:"created_at"`
	}
	result := make([]userResponse, 0, len(users))
	for _, u := range users {
		lastLogin := ""
		if u.LastLoginAt != nil {
			lastLogin = formatTimestamp(*u.LastLoginAt)
		}
		result = append(result, userResponse{
			ID:                 u.ID,
			Username:           u.Username,
			Role:               u.Role,
			CanCreateInstances: u.CanCreateInstances,
			LastLoginAt:        lastLogin,
			CreatedAt:          formatTimestamp(u.CreatedAt),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func CreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username           string `json:"username"`
		Password           string `json:"password"`
		Role               string `json:"role"`
		CanCreateInstances bool   `json:"can_create_instances"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	if body.Role == "" {
		body.Role = "user"
	}
	if body.Role != "admin" && body.Role != "user" {
		writeError(w, http.StatusBadRequest, "Role must be 'admin' or 'user'")
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	// Admins always can; the flag is only meaningful for the "user" role.
	canCreate := body.CanCreateInstances
	if body.Role == "admin" {
		canCreate = false
	}

	user := &database.User{
		Username:           body.Username,
		PasswordHash:       hash,
		Role:               body.Role,
		CanCreateInstances: canCreate,
	}
	if err := database.CreateUser(user); err != nil {
		writeError(w, http.StatusConflict, "Username already exists")
		return
	}

	var totalUsers int64
	database.DB.Model(&database.User{}).Count(&totalUsers)
	var assigned int64
	database.DB.Model(&database.UserInstance{}).Where("user_id = ?", user.ID).Count(&assigned)
	analytics.Track(r.Context(), analytics.EventUserCreated, map[string]any{
		"total_users":        totalUsers,
		"user_id":            user.ID,
		"role":               user.Role,
		"assigned_instances": assigned,
	})

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":                   user.ID,
		"username":             user.Username,
		"role":                 user.Role,
		"can_create_instances": user.CanCreateInstances,
	})
}

// UpdateUserPermissions toggles per-user permission flags (e.g. CanCreateInstances).
func UpdateUserPermissions(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	var body struct {
		CanCreateInstances *bool `json:"can_create_instances,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	updates := map[string]interface{}{}
	if body.CanCreateInstances != nil {
		updates["can_create_instances"] = *body.CanCreateInstances
	}
	if len(updates) == 0 {
		writeError(w, http.StatusBadRequest, "No fields to update")
		return
	}

	if err := database.DB.Model(&database.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update permissions")
		return
	}

	analyticsTrackUserUpdated(r, uint(id))

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.ID == uint(id) {
		writeError(w, http.StatusBadRequest, "Cannot delete your own account")
		return
	}

	if err := database.DeleteUser(uint(id)); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete user")
		return
	}

	// Invalidate all sessions for the deleted user
	SessionStore.DeleteByUserID(uint(id))

	var remaining int64
	database.DB.Model(&database.User{}).Count(&remaining)
	analytics.Track(r.Context(), analytics.EventUserDeleted, map[string]any{
		"remaining_users": remaining,
	})

	w.WriteHeader(http.StatusNoContent)
}

func UpdateUserRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Role != "admin" && body.Role != "user" {
		writeError(w, http.StatusBadRequest, "Role must be 'admin' or 'user'")
		return
	}

	currentUser := middleware.GetUser(r)
	if currentUser != nil && currentUser.ID == uint(id) && body.Role != "admin" {
		writeError(w, http.StatusBadRequest, "Cannot demote your own account")
		return
	}

	if err := database.DB.Model(&database.User{}).Where("id = ?", id).Update("role", body.Role).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update role")
		return
	}

	analyticsTrackUserUpdated(r, uint(id))

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// analyticsTrackUserUpdated emits a user_updated event with the canonical
// prop set used for both permission and role updates.
func analyticsTrackUserUpdated(r *http.Request, userID uint) {
	var u database.User
	if err := database.DB.First(&u, userID).Error; err != nil {
		return
	}
	var totalUsers int64
	database.DB.Model(&database.User{}).Count(&totalUsers)
	var assigned int64
	database.DB.Model(&database.UserInstance{}).Where("user_id = ?", userID).Count(&assigned)
	analytics.Track(r.Context(), analytics.EventUserUpdated, map[string]any{
		"total_users":        totalUsers,
		"user_id":            u.ID,
		"role":               u.Role,
		"assigned_instances": assigned,
	})
}

func GetUserAssignedInstances(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	instanceIDs, err := database.GetUserInstances(uint(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to get instances")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"instance_ids": instanceIDs})
}

func SetUserAssignedInstances(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	var body struct {
		InstanceIDs []uint `json:"instance_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := database.SetUserInstances(uint(id), body.InstanceIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to set instances")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Password == "" {
		writeError(w, http.StatusBadRequest, "Password is required")
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	if err := database.UpdateUserPassword(uint(id), hash); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update password")
		return
	}

	// Invalidate all sessions for this user
	SessionStore.DeleteByUserID(uint(id))

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
