package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gluk-w/claworc/llm-proxy/internal/config"
	"github.com/gluk-w/claworc/llm-proxy/internal/database"
)

func setupTestDB(t *testing.T) func() {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "proxy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	config.Cfg.DatabasePath = filepath.Join(tmpDir, "test.db")

	if err := database.Init(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to init database: %v", err)
	}

	return func() {
		database.Close()
		os.RemoveAll(tmpDir)
	}
}

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name      string
		headers   map[string]string
		wantToken string
	}{
		{
			name:      "Bearer token",
			headers:   map[string]string{"Authorization": "Bearer abc123"},
			wantToken: "abc123",
		},
		{
			name:      "x-api-key",
			headers:   map[string]string{"x-api-key": "sk-ant-test"},
			wantToken: "sk-ant-test",
		},
		{
			name:      "x-goog-api-key",
			headers:   map[string]string{"x-goog-api-key": "AIza-test"},
			wantToken: "AIza-test",
		},
		{
			name:      "no token",
			headers:   map[string]string{},
			wantToken: "",
		},
		{
			name:      "Authorization without Bearer",
			headers:   map[string]string{"Authorization": "Basic abc"},
			wantToken: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			token := extractToken(req)
			if token != tt.wantToken {
				t.Errorf("extractToken() = %s, want %s", token, tt.wantToken)
			}
		})
	}
}

func TestLookupToken(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Create a test token
	database.DB.Create(&database.InstanceToken{
		InstanceName: "bot-test",
		Token:        "valid-token",
		Enabled:      true,
	})

	// Create a disabled token (use raw SQL to force Enabled=false)
	database.DB.Exec("INSERT INTO instance_tokens (instance_name, token, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		"bot-disabled", "disabled-token", false, time.Now(), time.Now())

	tests := []struct {
		name     string
		token    string
		wantName string
		wantOK   bool
	}{
		{
			name:     "valid enabled token",
			token:    "valid-token",
			wantName: "bot-test",
			wantOK:   true,
		},
		{
			name:   "disabled token",
			token:  "disabled-token",
			wantOK: false,
		},
		{
			name:   "unknown token",
			token:  "unknown",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear cache before each lookup
			InvalidateAllTokenCache()

			// Verify token state in DB for debugging
			if tt.token == "disabled-token" {
				var dbToken database.InstanceToken
				if err := database.DB.Where("token = ?", tt.token).First(&dbToken).Error; err == nil {
					if dbToken.Enabled {
						t.Fatal("Test setup error: disabled-token should be disabled in DB")
					}
				}
			}

			name, ok := lookupToken(tt.token)
			if ok != tt.wantOK {
				t.Errorf("lookupToken() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && name != tt.wantName {
				t.Errorf("lookupToken() name = %s, want %s", name, tt.wantName)
			}
		})
	}
}

func TestAuthMiddleware_Isolated(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	database.DB.Create(&database.InstanceToken{
		InstanceName: "bot-test",
		Token:        "valid-token",
		Enabled:      true,
	})

	tests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{
			name:       "valid token",
			token:      "valid-token",
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid token",
			token:      "invalid-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing token",
			token:      "",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InvalidateAllTokenCache()

			// Create a test handler that returns 200 OK if auth passes
			handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Expected status %d, got %d", tt.wantStatus, w.Code)
			}
		})
	}
}

func TestTokenCache(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	database.DB.Create(&database.InstanceToken{
		InstanceName: "bot-test",
		Token:        "cache-test",
		Enabled:      true,
	})

	// First lookup - should hit DB
	name1, ok1 := lookupToken("cache-test")
	if !ok1 || name1 != "bot-test" {
		t.Fatal("First lookup failed")
	}

	// Second lookup - should hit cache (we'll verify by disabling in DB)
	database.DB.Model(&database.InstanceToken{}).Where("token = ?", "cache-test").Update("enabled", false)

	// Lookup should still return true (cached)
	name2, ok2 := lookupToken("cache-test")
	if !ok2 || name2 != "bot-test" {
		t.Error("Cache lookup failed - should still be cached")
	}

	// Invalidate and lookup again - should now return false
	InvalidateTokenCache("cache-test")
	_, ok3 := lookupToken("cache-test")
	if ok3 {
		t.Error("After invalidation, disabled token should not pass")
	}
}
