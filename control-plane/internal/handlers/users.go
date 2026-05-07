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

	// Bulk-load team memberships, teams, instance grants, and instances
	// so the response includes per-user access info without N+1 queries.
	var memberships []database.TeamMember
	database.DB.Find(&memberships)
	var teamRows []database.Team
	database.DB.Find(&teamRows)
	teamByID := make(map[uint]database.Team, len(teamRows))
	for _, t := range teamRows {
		teamByID[t.ID] = t
	}
	var grants []database.UserInstance
	database.DB.Find(&grants)
	var instances []database.Instance
	database.DB.Select("id, name, display_name, team_id").Find(&instances)
	instByID := make(map[uint]database.Instance, len(instances))
	for _, inst := range instances {
		instByID[inst.ID] = inst
	}

	type teamRef struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	type instanceRef struct {
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		TeamID      uint   `json:"team_id"`
		TeamName    string `json:"team_name"`
	}
	teamsByUser := make(map[uint][]teamRef, len(users))
	for _, m := range memberships {
		t, ok := teamByID[m.TeamID]
		if !ok {
			continue
		}
		teamsByUser[m.UserID] = append(teamsByUser[m.UserID], teamRef{
			ID: t.ID, Name: t.Name, Role: m.Role,
		})
	}
	instancesByUser := make(map[uint][]instanceRef, len(users))
	for _, g := range grants {
		inst, ok := instByID[g.InstanceID]
		if !ok {
			continue
		}
		teamName := ""
		if t, ok := teamByID[inst.TeamID]; ok {
			teamName = t.Name
		}
		instancesByUser[g.UserID] = append(instancesByUser[g.UserID], instanceRef{
			ID: inst.ID, Name: inst.Name, DisplayName: inst.DisplayName,
			TeamID: inst.TeamID, TeamName: teamName,
		})
	}

	type userResponse struct {
		ID          uint          `json:"id"`
		Username    string        `json:"username"`
		Role        string        `json:"role"`
		LastLoginAt string        `json:"last_login_at"`
		CreatedAt   string        `json:"created_at"`
		Teams       []teamRef     `json:"teams"`
		Instances   []instanceRef `json:"instances"`
	}
	result := make([]userResponse, 0, len(users))
	for _, u := range users {
		lastLogin := ""
		if u.LastLoginAt != nil {
			lastLogin = formatTimestamp(*u.LastLoginAt)
		}
		t := teamsByUser[u.ID]
		if t == nil {
			t = []teamRef{}
		}
		ins := instancesByUser[u.ID]
		if ins == nil {
			ins = []instanceRef{}
		}
		result = append(result, userResponse{
			ID:          u.ID,
			Username:    u.Username,
			Role:        u.Role,
			LastLoginAt: lastLogin,
			CreatedAt:   formatTimestamp(u.CreatedAt),
			Teams:       t,
			Instances:   ins,
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

// GetUserTeamsHandler returns the team memberships of a user, keyed by team
// ID with the per-team role attached. Used by the user editor to round-trip
// existing grants.
func GetUserTeamsHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}
	memberships, err := database.GetUserTeams(uint(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load teams")
		return
	}
	type item struct {
		TeamID uint   `json:"team_id"`
		Name   string `json:"name"`
		Role   string `json:"role"`
	}
	out := make([]item, len(memberships))
	for i, m := range memberships {
		out[i] = item{TeamID: m.ID, Name: m.Name, Role: m.Role}
	}
	writeJSON(w, http.StatusOK, out)
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
