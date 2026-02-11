package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

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
		ID        uint   `json:"id"`
		Username  string `json:"username"`
		Role      string `json:"role"`
		CreatedAt string `json:"created_at"`
	}
	result := make([]userResponse, 0, len(users))
	for _, u := range users {
		result = append(result, userResponse{
			ID:        u.ID,
			Username:  u.Username,
			Role:      u.Role,
			CreatedAt: formatTimestamp(u.CreatedAt),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func CreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
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

	user := &database.User{
		Username:     body.Username,
		PasswordHash: hash,
		Role:         body.Role,
	}
	if err := database.CreateUser(user); err != nil {
		writeError(w, http.StatusConflict, "Username already exists")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
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

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
