package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

func TestHealthCheck_WithDB(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	HealthCheck(w, httptest.NewRequest("GET", "/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["status"] != "healthy" {
		t.Errorf("status = %q, want healthy", body["status"])
	}
	if body["database"] != "connected" {
		t.Errorf("database = %q, want connected", body["database"])
	}
}

func TestHealthCheck_NoDB(t *testing.T) {
	// Don't set up DB
	origDB := database.DB
	database.DB = nil
	t.Cleanup(func() { database.DB = origDB })

	w := httptest.NewRecorder()
	HealthCheck(w, httptest.NewRequest("GET", "/health", nil))

	var body map[string]string
	json.Unmarshal(w.Body.Bytes(), &body)

	if body["status"] != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", body["status"])
	}
	if body["database"] != "disconnected" {
		t.Errorf("database = %q, want disconnected", body["database"])
	}
}
