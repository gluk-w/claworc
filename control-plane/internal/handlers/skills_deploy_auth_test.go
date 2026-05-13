package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// setupDeployAuthDB extends setupTestDB with the team-related tables that the
// authorization path inside DeploySkill needs.
func setupDeployAuthDB(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.Team{}, &database.TeamMember{}); err != nil {
		t.Fatalf("migrate team tables: %v", err)
	}
}

func seedInstanceInTeam(t *testing.T, name string, teamID uint) database.Instance {
	t.Helper()
	inst := database.Instance{
		Name:        name,
		DisplayName: name,
		Status:      "running",
		TeamID:      teamID,
	}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return inst
}

func seedTeam(t *testing.T, name string) database.Team {
	t.Helper()
	team := database.Team{Name: name}
	if err := database.DB.Create(&team).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
	return team
}

func seedUserWithRole(t *testing.T, username, role string) *database.User {
	t.Helper()
	u := &database.User{Username: username, PasswordHash: "x", Role: role}
	if err := database.DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func addTeamManager(t *testing.T, teamID, userID uint) {
	t.Helper()
	m := &database.TeamMember{TeamID: teamID, UserID: userID, Role: database.TeamRoleManager}
	if err := database.DB.Create(m).Error; err != nil {
		t.Fatalf("create team member: %v", err)
	}
}

func deployRequest(t *testing.T, slug string, instanceIDs []uint, user *database.User) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"instance_ids": instanceIDs,
		"source":       "library",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	r := authedRequest(http.MethodPost, "/api/v1/skills/"+slug+"/deploy", body, user)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	DeploySkill(w, r)
	return w
}

func TestDeploySkill_RegularUserForbidden(t *testing.T) {
	setupDeployAuthDB(t)
	team := seedTeam(t, "alpha")
	inst := seedInstanceInTeam(t, "i1", team.ID)
	user := seedUserWithRole(t, "regular", "user")

	w := deployRequest(t, "any-slug", []uint{inst.ID}, user)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestDeploySkill_ManagerForeignTeamForbidden(t *testing.T) {
	setupDeployAuthDB(t)
	teamA := seedTeam(t, "alpha")
	teamB := seedTeam(t, "beta")
	instB := seedInstanceInTeam(t, "ib", teamB.ID)
	mgr := seedUserWithRole(t, "mgr-a", "user")
	addTeamManager(t, teamA.ID, mgr.ID)

	w := deployRequest(t, "any-slug", []uint{instB.ID}, mgr)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-team deploy, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// Manager-of-own-team and admin paths pass the per-instance auth check; the
// handler then attempts to load the (non-existent) skill from disk and fails
// later. We assert the status is NOT 403, which is enough to prove the auth
// gate accepted the caller.
func TestDeploySkill_ManagerOwnTeamPassesAuth(t *testing.T) {
	setupDeployAuthDB(t)
	team := seedTeam(t, "alpha")
	inst := seedInstanceInTeam(t, "ia", team.ID)
	mgr := seedUserWithRole(t, "mgr-a", "user")
	addTeamManager(t, team.ID, mgr.ID)

	w := deployRequest(t, "definitely-not-a-real-skill", []uint{inst.ID}, mgr)
	if w.Code == http.StatusForbidden {
		t.Fatalf("manager-of-team deploy should not be 403; body=%s", w.Body.String())
	}
}

func TestDeploySkill_AdminPassesAuth(t *testing.T) {
	setupDeployAuthDB(t)
	team := seedTeam(t, "alpha")
	inst := seedInstanceInTeam(t, "ia", team.ID)
	admin := seedUserWithRole(t, "admin", "admin")

	w := deployRequest(t, "definitely-not-a-real-skill", []uint{inst.ID}, admin)
	if w.Code == http.StatusForbidden {
		t.Fatalf("admin deploy should not be 403; body=%s", w.Body.String())
	}
}

func TestDeploySkill_AnyForbiddenInstanceBlocksDeploy(t *testing.T) {
	setupDeployAuthDB(t)
	teamA := seedTeam(t, "alpha")
	teamB := seedTeam(t, "beta")
	instA := seedInstanceInTeam(t, "ia", teamA.ID)
	instB := seedInstanceInTeam(t, "ib", teamB.ID)
	mgr := seedUserWithRole(t, "mgr-a", "user")
	addTeamManager(t, teamA.ID, mgr.ID)

	w := deployRequest(t, "any-slug", []uint{instA.ID, instB.ID}, mgr)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when any target is outside manager's teams, got %d (body=%s)", w.Code, w.Body.String())
	}
}
