package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// --- AuthConfig ---

func TestAuthConfig_Disabled(t *testing.T) {
	w := httptest.NewRecorder()
	AuthConfig(w, httptest.NewRequest("GET", "/api/v1/auth/config", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["cf_access_enabled"] != false {
		t.Errorf("cf_access_enabled = %v, want false", body["cf_access_enabled"])
	}
	if _, ok := body["logout_url"]; ok {
		t.Error("logout_url should be absent when CF disabled")
	}
}

func TestAuthConfig_Enabled(t *testing.T) {
	config.Cfg.CFAccessEnabled = true
	config.Cfg.CFAccessTeamDomain = "https://team.cloudflareaccess.com"
	t.Cleanup(func() {
		config.Cfg.CFAccessEnabled = false
		config.Cfg.CFAccessTeamDomain = ""
	})

	w := httptest.NewRecorder()
	AuthConfig(w, httptest.NewRequest("GET", "/api/v1/auth/config", nil))

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["cf_access_enabled"] != true {
		t.Errorf("cf_access_enabled = %v, want true", body["cf_access_enabled"])
	}
	if body["logout_url"] != "https://team.cloudflareaccess.com/cdn-cgi/access/logout" {
		t.Errorf("logout_url = %v, want CF logout URL", body["logout_url"])
	}
}

// --- Built-in login disabled in CF mode ---

func TestLogin_BlockedWhenCFEnabled(t *testing.T) {
	setupAuthTest(t)
	createUserWithPassword(t, "alice", "secret123", "admin")
	config.Cfg.CFAccessEnabled = true
	t.Cleanup(func() { config.Cfg.CFAccessEnabled = false })

	w := httptest.NewRecorder()
	Login(w, postJSON("/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "secret123",
	}))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// --- CreateUser email handling ---

func TestCreateUser_WithEmail(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	CreateUser(w, postJSON("/api/v1/users", map[string]string{
		"username": "newuser",
		"email":    "  New@Example.com ",
		"password": "p",
		"role":     "user",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	// Email is normalized on store.
	u, err := database.GetUserByEmail("new@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if u.Username != "newuser" {
		t.Errorf("username = %q, want newuser", u.Username)
	}
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	setupAuthTest(t)
	database.CreateUser(&database.User{Username: "first", Email: "dup@example.com", PasswordHash: "h", Role: "user"})

	w := httptest.NewRecorder()
	CreateUser(w, postJSON("/api/v1/users", map[string]string{
		"username": "second",
		"email":    "DUP@example.com", // same email, different case
		"password": "p",
	}))

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}

// --- UpdateUserEmail ---

func TestUpdateUserEmail(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")
	target := createUserWithPassword(t, "target", "p", "user")

	w := httptest.NewRecorder()
	req := withChiAndUser(
		postJSON("/api/v1/users/x/email", map[string]string{"email": "Target@Example.com"}),
		admin, map[string]string{"userId": fmt.Sprint(target.ID)},
	)
	UpdateUserEmail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got, _ := database.GetUserByID(target.ID)
	if got.Email != "target@example.com" {
		t.Errorf("email = %q, want normalized target@example.com", got.Email)
	}
}

func TestUpdateUserEmail_DuplicateRejected(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")
	database.CreateUser(&database.User{Username: "owner", Email: "taken@example.com", PasswordHash: "h", Role: "user"})
	target := createUserWithPassword(t, "target", "p", "user")

	w := httptest.NewRecorder()
	req := withChiAndUser(
		postJSON("/api/v1/users/x/email", map[string]string{"email": "taken@example.com"}),
		admin, map[string]string{"userId": fmt.Sprint(target.ID)},
	)
	UpdateUserEmail(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}

func TestUpdateUserEmail_ClearAndReuse(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")
	target := createUserWithPassword(t, "target", "p", "user")
	database.UpdateUserEmail(target.ID, "current@example.com")

	// Setting the same email on the same user must be allowed (excludeID).
	w := httptest.NewRecorder()
	req := withChiAndUser(
		postJSON("/api/v1/users/x/email", map[string]string{"email": "current@example.com"}),
		admin, map[string]string{"userId": fmt.Sprint(target.ID)},
	)
	UpdateUserEmail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("re-setting own email: status = %d, want 200", w.Code)
	}

	// Clearing the email.
	w = httptest.NewRecorder()
	req = withChiAndUser(
		postJSON("/api/v1/users/x/email", map[string]string{"email": ""}),
		admin, map[string]string{"userId": fmt.Sprint(target.ID)},
	)
	UpdateUserEmail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("clearing email: status = %d, want 200", w.Code)
	}
	got, _ := database.GetUserByID(target.ID)
	if got.Email != "" {
		t.Errorf("email = %q, want empty after clear", got.Email)
	}
}
