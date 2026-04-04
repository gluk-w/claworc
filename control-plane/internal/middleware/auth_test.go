package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&database.User{}, &database.UserInstance{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() {
		database.DB = nil
		config.Cfg.AuthDisabled = false
	})
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r)
		if user != nil {
			json.NewEncoder(w).Encode(map[string]string{"username": user.Username})
		} else {
			w.WriteHeader(http.StatusOK)
		}
	})
}

func TestRequireAuth_ValidSession(t *testing.T) {
	setupTestDB(t)
	store := auth.NewSessionStore()
	database.CreateUser(&database.User{Username: "alice", PasswordHash: "h", Role: "admin"})
	user, _ := database.GetUserByUsername("alice")
	sessionID, _ := store.Create(user.ID)

	handler := RequireAuth(store)(okHandler())
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["username"] != "alice" {
		t.Errorf("username = %q, want %q", body["username"], "alice")
	}
}

func TestRequireAuth_NoCookie(t *testing.T) {
	setupTestDB(t)
	store := auth.NewSessionStore()
	handler := RequireAuth(store)(okHandler())

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRequireAuth_InvalidSession(t *testing.T) {
	setupTestDB(t)
	store := auth.NewSessionStore()
	handler := RequireAuth(store)(okHandler())

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "bogus-session-id"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRequireAuth_DeletedUser(t *testing.T) {
	setupTestDB(t)
	store := auth.NewSessionStore()
	database.CreateUser(&database.User{Username: "ghost", PasswordHash: "h", Role: "user"})
	user, _ := database.GetUserByUsername("ghost")
	sessionID, _ := store.Create(user.ID)
	database.DeleteUser(user.ID)

	handler := RequireAuth(store)(okHandler())
	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionID})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRequireAuth_AuthDisabled(t *testing.T) {
	setupTestDB(t)
	config.Cfg.AuthDisabled = true
	database.CreateUser(&database.User{Username: "admin", PasswordHash: "h", Role: "admin"})

	store := auth.NewSessionStore()
	handler := RequireAuth(store)(okHandler())

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["username"] != "admin" {
		t.Errorf("username = %q, want %q", body["username"], "admin")
	}
}

func TestRequireAuth_AuthDisabled_NoAdmin(t *testing.T) {
	setupTestDB(t)
	config.Cfg.AuthDisabled = true

	store := auth.NewSessionStore()
	handler := RequireAuth(store)(okHandler())

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestRequireAdmin_AdminUser(t *testing.T) {
	admin := &database.User{Username: "admin", Role: "admin"}
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithUser(req.Context(), admin))

	w := httptest.NewRecorder()
	RequireAdmin(okHandler()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("admin: status = %d, want 200", w.Code)
	}
}

func TestRequireAdmin_RegularUser(t *testing.T) {
	user := &database.User{Username: "user", Role: "user"}
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithUser(req.Context(), user))

	w := httptest.NewRecorder()
	RequireAdmin(okHandler()).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("regular user: status = %d, want 403", w.Code)
	}
}

func TestRequireAdmin_NoUser(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	RequireAdmin(okHandler()).ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("no user: status = %d, want 403", w.Code)
	}
}

func TestCanAccessInstance_Admin(t *testing.T) {
	setupTestDB(t)
	admin := &database.User{Username: "admin", Role: "admin"}
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithUser(req.Context(), admin))

	if !CanAccessInstance(req, 999) {
		t.Error("admin should access any instance")
	}
}

func TestCanAccessInstance_AssignedUser(t *testing.T) {
	setupTestDB(t)
	database.CreateUser(&database.User{Username: "u", PasswordHash: "h", Role: "user"})
	user, _ := database.GetUserByUsername("u")
	database.SetUserInstances(user.ID, []uint{5})

	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithUser(req.Context(), user))

	if !CanAccessInstance(req, 5) {
		t.Error("user should access assigned instance")
	}
}

func TestCanAccessInstance_UnassignedUser(t *testing.T) {
	setupTestDB(t)
	database.CreateUser(&database.User{Username: "u", PasswordHash: "h", Role: "user"})
	user, _ := database.GetUserByUsername("u")

	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithUser(req.Context(), user))

	if CanAccessInstance(req, 5) {
		t.Error("user should not access unassigned instance")
	}
}

func TestCanAccessInstance_NoUser(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if CanAccessInstance(req, 1) {
		t.Error("should return false with no user in context")
	}
}

func TestGetUser_FromContext(t *testing.T) {
	t.Parallel()
	user := &database.User{Username: "test"}
	req := httptest.NewRequest("GET", "/", nil)

	if GetUser(req) != nil {
		t.Error("GetUser should return nil when not set")
	}

	req = req.WithContext(WithUser(req.Context(), user))
	got := GetUser(req)
	if got == nil || got.Username != "test" {
		t.Error("GetUser should return the user set in context")
	}
}
