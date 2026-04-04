package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

func withChiAndUser(r *http.Request, user *database.User, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := r.Context()
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	ctx = middleware.WithUser(ctx, user)
	return r.WithContext(ctx)
}

// --- ListUsers ---

func TestListUsers(t *testing.T) {
	setupAuthTest(t)
	createUserWithPassword(t, "alice", "p", "admin")
	createUserWithPassword(t, "bob", "p", "user")

	w := httptest.NewRecorder()
	ListUsers(w, httptest.NewRequest("GET", "/api/v1/users", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if len(body) != 2 {
		t.Errorf("len = %d, want 2", len(body))
	}
}

// --- CreateUser ---

func TestCreateUser_Success(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	CreateUser(w, postJSON("/api/v1/users", map[string]string{
		"username": "newuser",
		"password": "pass123",
		"role":     "user",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["username"] != "newuser" {
		t.Errorf("username = %v, want newuser", body["username"])
	}
	if body["role"] != "user" {
		t.Errorf("role = %v, want user", body["role"])
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	setupAuthTest(t)
	createUserWithPassword(t, "alice", "p", "admin")

	w := httptest.NewRecorder()
	CreateUser(w, postJSON("/api/v1/users", map[string]string{
		"username": "alice",
		"password": "pass",
	}))

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}

func TestCreateUser_InvalidRole(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	CreateUser(w, postJSON("/api/v1/users", map[string]string{
		"username": "bob",
		"password": "pass",
		"role":     "superadmin",
	}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCreateUser_DefaultRole(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	CreateUser(w, postJSON("/api/v1/users", map[string]string{
		"username": "charlie",
		"password": "pass",
	}))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["role"] != "user" {
		t.Errorf("default role = %v, want user", body["role"])
	}
}

func TestCreateUser_EmptyFields(t *testing.T) {
	setupAuthTest(t)

	w := httptest.NewRecorder()
	CreateUser(w, postJSON("/api/v1/users", map[string]string{}))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// --- DeleteUser ---

func TestDeleteUser_CannotDeleteSelf(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")

	req := httptest.NewRequest("DELETE", "/api/v1/users/"+fmt.Sprint(admin.ID), nil)
	req = withChiAndUser(req, admin, map[string]string{"userId": fmt.Sprint(admin.ID)})

	w := httptest.NewRecorder()
	DeleteUser(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDeleteUser_InvalidatesSessions(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")
	target := createUserWithPassword(t, "target", "p", "user")
	targetSession, _ := SessionStore.Create(target.ID)

	req := httptest.NewRequest("DELETE", "/api/v1/users/"+fmt.Sprint(target.ID), nil)
	req = withChiAndUser(req, admin, map[string]string{"userId": fmt.Sprint(target.ID)})

	w := httptest.NewRecorder()
	DeleteUser(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}

	// Session should be invalidated
	_, ok := SessionStore.Get(targetSession)
	if ok {
		t.Error("deleted user's session still valid")
	}
}

// --- UpdateUserRole ---

func TestUpdateUserRole_CannotDemoteSelf(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")

	req := postJSON("/api/v1/users/"+fmt.Sprint(admin.ID)+"/role", map[string]string{
		"role": "user",
	})
	req = withChiAndUser(req, admin, map[string]string{"userId": fmt.Sprint(admin.ID)})

	w := httptest.NewRecorder()
	UpdateUserRole(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestUpdateUserRole_Success(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")
	target := createUserWithPassword(t, "user", "p", "user")

	req := postJSON("/api/v1/users/"+fmt.Sprint(target.ID)+"/role", map[string]string{
		"role": "admin",
	})
	req = withChiAndUser(req, admin, map[string]string{"userId": fmt.Sprint(target.ID)})

	w := httptest.NewRecorder()
	UpdateUserRole(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	updated, _ := database.GetUserByID(target.ID)
	if updated.Role != "admin" {
		t.Errorf("role = %q, want admin", updated.Role)
	}
}

func TestUpdateUserRole_InvalidRole(t *testing.T) {
	setupAuthTest(t)
	admin := createUserWithPassword(t, "admin", "p", "admin")
	target := createUserWithPassword(t, "user", "p", "user")

	req := postJSON("/api/v1/users/"+fmt.Sprint(target.ID)+"/role", map[string]string{
		"role": "superadmin",
	})
	req = withChiAndUser(req, admin, map[string]string{"userId": fmt.Sprint(target.ID)})

	w := httptest.NewRecorder()
	UpdateUserRole(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// --- ResetUserPassword ---

func TestResetUserPassword_InvalidatesSessions(t *testing.T) {
	setupAuthTest(t)
	target := createUserWithPassword(t, "target", "old", "user")
	targetSession, _ := SessionStore.Create(target.ID)

	admin := createUserWithPassword(t, "admin", "p", "admin")
	req := postJSON("/api/v1/users/"+fmt.Sprint(target.ID)+"/reset-password", map[string]string{
		"password": "newpass",
	})
	req = withChiAndUser(req, admin, map[string]string{"userId": fmt.Sprint(target.ID)})

	w := httptest.NewRecorder()
	ResetUserPassword(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// Old session invalidated
	_, ok := SessionStore.Get(targetSession)
	if ok {
		t.Error("target's session should be invalidated")
	}

	// New password works
	dbUser, _ := database.GetUserByID(target.ID)
	if !auth.CheckPassword("newpass", dbUser.PasswordHash) {
		t.Error("new password doesn't work")
	}
}

func TestResetUserPassword_EmptyPassword(t *testing.T) {
	setupAuthTest(t)
	target := createUserWithPassword(t, "target", "old", "user")
	admin := createUserWithPassword(t, "admin", "p", "admin")

	req := postJSON("/api/v1/users/"+fmt.Sprint(target.ID)+"/reset-password", map[string]string{
		"password": "",
	})
	req = withChiAndUser(req, admin, map[string]string{"userId": fmt.Sprint(target.ID)})

	w := httptest.NewRecorder()
	ResetUserPassword(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
