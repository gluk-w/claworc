package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

type teamResponse struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	MemberCount   int64  `json:"member_count"`
	InstanceCount int64  `json:"instance_count"`
}

func teamToResponse(t database.Team) teamResponse {
	return teamResponse{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
	}
}

// teamCounts returns per-team member and instance counts as maps keyed by
// team ID. The counts are computed via two grouped queries.
func teamCounts() (members, instances map[uint]int64) {
	type row struct {
		TeamID uint
		N      int64
	}
	members = map[uint]int64{}
	instances = map[uint]int64{}
	var mrows []row
	database.DB.Model(&database.TeamMember{}).
		Select("team_id, COUNT(*) AS n").Group("team_id").Scan(&mrows)
	for _, r := range mrows {
		members[r.TeamID] = r.N
	}
	var irows []row
	database.DB.Model(&database.Instance{}).
		Select("team_id, COUNT(*) AS n").Group("team_id").Scan(&irows)
	for _, r := range irows {
		instances[r.TeamID] = r.N
	}
	return members, instances
}

// ListTeams returns all teams the caller can see. Admins get every team;
// non-admins get only the teams they belong to.
func ListTeams(w http.ResponseWriter, r *http.Request) {
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	memberCounts, instanceCounts := teamCounts()

	if user.Role == "admin" {
		teams, err := database.ListTeams()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list teams")
			return
		}
		out := make([]teamResponse, len(teams))
		for i, t := range teams {
			resp := teamToResponse(t)
			resp.MemberCount = memberCounts[t.ID]
			resp.InstanceCount = instanceCounts[t.ID]
			out[i] = resp
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	memberships, err := database.GetUserTeams(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list teams")
		return
	}
	out := make([]teamResponse, len(memberships))
	for i, m := range memberships {
		resp := teamToResponse(m.Team)
		resp.MemberCount = memberCounts[m.Team.ID]
		resp.InstanceCount = instanceCounts[m.Team.ID]
		out[i] = resp
	}
	writeJSON(w, http.StatusOK, out)
}

type teamCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// CreateTeam creates a new team (admin-only).
func CreateTeam(w http.ResponseWriter, r *http.Request) {
	var body teamCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	t := database.Team{Name: body.Name, Description: body.Description}
	if err := database.CreateTeam(&t); err != nil {
		writeError(w, http.StatusBadRequest, "Failed to create team: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, teamToResponse(t))
}

// UpdateTeam updates a team's name/description (admin-only).
func UpdateTeam(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid team ID")
		return
	}
	var body teamCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	updates := map[string]interface{}{}
	if name := strings.TrimSpace(body.Name); name != "" {
		updates["name"] = name
	}
	updates["description"] = body.Description
	if err := database.UpdateTeam(uint(id), updates); err != nil {
		writeError(w, http.StatusBadRequest, "Failed to update team: "+err.Error())
		return
	}
	t, err := database.GetTeam(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Team not found")
		return
	}
	writeJSON(w, http.StatusOK, teamToResponse(*t))
}

// DeleteTeam removes a team (admin-only). A team with attached
// instances must be cleared first.
func DeleteTeam(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid team ID")
		return
	}
	if err := database.DeleteTeam(uint(id)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTeamMembers returns the user-role pairs for a team (admin-only).
func ListTeamMembers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid team ID")
		return
	}
	members, err := database.ListTeamMembers(uint(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list members")
		return
	}
	writeJSON(w, http.StatusOK, members)
}

type teamMemberRequest struct {
	UserID uint   `json:"user_id"`
	Role   string `json:"role"`
}

// SetTeamMember adds or updates a team membership. Pass role="" to remove.
func SetTeamMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid team ID")
		return
	}
	var body teamMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if body.UserID == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if err := database.SetTeamMember(uint(id), body.UserID, body.Role); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveTeamMember removes a member from a team.
func RemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid team ID")
		return
	}
	uid, err := strconv.Atoi(chi.URLParam(r, "userId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}
	if err := database.SetTeamMember(uint(id), uint(uid), ""); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetTeamProviders returns the list of global LLM provider IDs whitelisted
// for the team.
func GetTeamProviders(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid team ID")
		return
	}
	ids, err := database.GetTeamProviderIDs(uint(id))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load providers")
		return
	}
	if ids == nil {
		ids = []uint{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"provider_ids": ids})
}

type teamProvidersRequest struct {
	ProviderIDs []uint `json:"provider_ids"`
}

// SetTeamProviders replaces the team's provider whitelist (admin-only).
// Only global providers (LLMProvider.InstanceID == nil) are accepted.
func SetTeamProviders(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid team ID")
		return
	}
	var body teamProvidersRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	for _, pid := range body.ProviderIDs {
		var p database.LLMProvider
		if err := database.DB.First(&p, pid).Error; err != nil {
			writeError(w, http.StatusBadRequest, "Unknown provider")
			return
		}
		if p.InstanceID != nil {
			writeError(w, http.StatusBadRequest, "Only global providers can be whitelisted to a team")
			return
		}
	}
	if err := database.SetTeamProviders(uint(id), body.ProviderIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update providers")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
