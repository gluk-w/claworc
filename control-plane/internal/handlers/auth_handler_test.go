package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
)

func setupAuthTest(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	SessionStore = auth.NewSessionStore()
	t.Cleanup(func() { SessionStore = nil })
}

func createUserWithPassword(t *testing.T, username, password, role string) *database.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := &database.User{Username: username, PasswordHash: hash, Role: role}
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func postJSON(url string, body interface{}) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// --- Login ---

func TestLogin_Success(t *testing.T) {
	setupAuthTest(t)
	createUserWithPassword(t, "alice", "secret123", "admin")

	w := httptest.NewRecorder()
	Login(w, postJSON("/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "secret123",
	}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	// Verify session cookie is set
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == auth.SessionCookie && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("session cookie not set")
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["username"] != "alice" {
		t.Errorf("username = %v, want alice", body["username"])
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	setupAuthTest(t)
	createUserWithPassword(t, "alice", "secret123", "admin")

	w := httptest.NewRecorder()
	Login(w, postJSON("/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "wrongpass",
	}))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	Login(w, postJSON("/api/v1/auth/login", map[string]string{
		"username": "nobody",
		"password": "pass",
	}))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestLogin_EmptyBody(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	Login(w, postJSON("/api/v1/auth/login", map[string]string{}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// --- Logout ---

func TestLogout_ClearsCookie(t *testing.T) {
	setupAuthTest(t)
	user := createUserWithPassword(t, "alice", "pass", "admin")
	sessionID, _ := SessionStore.Create(user.ID)

	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
	w := httptest.NewRecorder()
	Logout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Session should be deleted
	_, ok := SessionStore.Get(sessionID)
	if ok {
		t.Error("session still exists after logout")
	}
}

// --- SetupRequired ---

func TestSetupRequired_NoUsers(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	SetupRequired(w, httptest.NewRequest("GET", "/api/v1/auth/setup-required", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]bool
	json.Unmarshal(w.Body.Bytes(), &body)
	if !body["setup_required"] {
		t.Error("setup_required should be true when no users")
	}
}

func TestSetupRequired_HasUsers(t *testing.T) {
	setupAuthTest(t)
	createUserWithPassword(t, "admin", "pass", "admin")

	w := httptest.NewRecorder()
	SetupRequired(w, httptest.NewRequest("GET", "/api/v1/auth/setup-required", nil))

	var body map[string]bool
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["setup_required"] {
		t.Error("setup_required should be false when users exist")
	}
}

// --- SetupCreateAdmin ---

func TestSetupCreateAdmin_Success(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	SetupCreateAdmin(w, postJSON("/api/v1/auth/setup", map[string]string{
		"username": "admin",
		"password": "password123",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["role"] != "admin" {
		t.Errorf("role = %v, want admin", body["role"])
	}

	// Verify cookie set
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == auth.SessionCookie {
			found = true
		}
	}
	if !found {
		t.Error("session cookie not set")
	}
}

func TestSetupCreateAdmin_AlreadySetup(t *testing.T) {
	setupAuthTest(t)
	createUserWithPassword(t, "admin", "pass", "admin")

	w := httptest.NewRecorder()
	SetupCreateAdmin(w, postJSON("/api/v1/auth/setup", map[string]string{
		"username": "admin2",
		"password": "pass",
	}))

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}

func TestSetupCreateAdmin_EmptyFields(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	SetupCreateAdmin(w, postJSON("/api/v1/auth/setup", map[string]string{
		"username": "",
		"password": "",
	}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// --- ChangePassword ---

func TestChangePassword_Success(t *testing.T) {
	setupAuthTest(t)
	user := createUserWithPassword(t, "alice", "oldpass", "admin")
	sessionID, _ := SessionStore.Create(user.ID)
	// Create another session that should be invalidated
	otherSession, _ := SessionStore.Create(user.ID)

	req := postJSON("/api/v1/auth/change-password", map[string]string{
		"current_password": "oldpass",
		"new_password":     "newpass",
	})
	req = req.WithContext(middleware.WithUser(req.Context(), user))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})

	w := httptest.NewRecorder()
	ChangePassword(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// Current session should still work
	_, ok := SessionStore.Get(sessionID)
	if !ok {
		t.Error("current session should not be invalidated")
	}

	// Other session should be invalidated
	_, ok = SessionStore.Get(otherSession)
	if ok {
		t.Error("other session should be invalidated")
	}

	// New password should work
	dbUser, _ := database.GetUserByID(user.ID)
	if !auth.CheckPassword("newpass", dbUser.PasswordHash) {
		t.Error("new password doesn't work")
	}
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	setupAuthTest(t)
	user := createUserWithPassword(t, "alice", "oldpass", "admin")

	req := postJSON("/api/v1/auth/change-password", map[string]string{
		"current_password": "wrongpass",
		"new_password":     "newpass",
	})
	req = req.WithContext(middleware.WithUser(req.Context(), user))

	w := httptest.NewRecorder()
	ChangePassword(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// --- GetCurrentUser ---

func TestGetCurrentUser_Authenticated(t *testing.T) {
	setupAuthTest(t)
	user := &database.User{ID: 1, Username: "alice", Role: "admin"}

	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req = req.WithContext(middleware.WithUser(req.Context(), user))

	w := httptest.NewRecorder()
	GetCurrentUser(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["username"] != "alice" {
		t.Errorf("username = %v, want alice", body["username"])
	}
}

func TestGetCurrentUser_NoUser(t *testing.T) {
	w := httptest.NewRecorder()
	GetCurrentUser(w, httptest.NewRequest("GET", "/api/v1/auth/me", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
