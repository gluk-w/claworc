package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// newChiRequestWithBody creates an *http.Request with chi URL params and a JSON body.
func newChiRequestWithBody(method, path string, params map[string]string, body []byte) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestGetAllowedSourceIPs_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/ssh-allowed-ips", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	GetAllowedSourceIPs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetAllowedSourceIPs_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", "/api/v1/instances/999/ssh-allowed-ips", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetAllowedSourceIPs(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetAllowedSourceIPs_Empty(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-iprestrict", DisplayName: "IP Restrict", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetAllowedSourceIPs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp ipRestrictResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AllowedIPs != "" {
		t.Errorf("expected empty allowed_ips, got %q", resp.AllowedIPs)
	}
	if resp.InstanceID != inst.ID {
		t.Errorf("expected instance_id %d, got %d", inst.ID, resp.InstanceID)
	}
}

func TestGetAllowedSourceIPs_WithIPs(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:             "bot-iprestrict",
		DisplayName:      "IP Restrict",
		Status:           "running",
		AllowedSourceIPs: "10.0.0.1, 192.168.1.0/24",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetAllowedSourceIPs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp ipRestrictResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AllowedIPs != "10.0.0.1, 192.168.1.0/24" {
		t.Errorf("expected '10.0.0.1, 192.168.1.0/24', got %q", resp.AllowedIPs)
	}
	if resp.NormalizedList != "10.0.0.1, 192.168.1.0/24" {
		t.Errorf("expected normalized '10.0.0.1, 192.168.1.0/24', got %q", resp.NormalizedList)
	}
}

func TestUpdateAllowedSourceIPs_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: "10.0.0.1"})
	r := newChiRequestWithBody("PUT", "/api/v1/instances/abc/ssh-allowed-ips", map[string]string{"id": "abc"}, body)
	w := httptest.NewRecorder()

	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestUpdateAllowedSourceIPs_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: "10.0.0.1"})
	r := newChiRequestWithBody("PUT", "/api/v1/instances/999/ssh-allowed-ips", map[string]string{"id": "999"}, body)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUpdateAllowedSourceIPs_ValidSingleIP(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-iprestrict", DisplayName: "IP Restrict", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: "10.0.0.1"})
	r := newChiRequestWithBody("PUT", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)}, body)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp ipRestrictResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AllowedIPs != "10.0.0.1" {
		t.Errorf("expected '10.0.0.1', got %q", resp.AllowedIPs)
	}

	// Verify it persisted in DB
	var updated database.Instance
	database.DB.First(&updated, inst.ID)
	if updated.AllowedSourceIPs != "10.0.0.1" {
		t.Errorf("expected DB to have '10.0.0.1', got %q", updated.AllowedSourceIPs)
	}
}

func TestUpdateAllowedSourceIPs_ValidCIDR(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-iprestrict", DisplayName: "IP Restrict", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: "192.168.1.0/24, 10.0.0.0/8"})
	r := newChiRequestWithBody("PUT", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)}, body)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp ipRestrictResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.AllowedIPs != "192.168.1.0/24, 10.0.0.0/8" {
		t.Errorf("expected '192.168.1.0/24, 10.0.0.0/8', got %q", resp.AllowedIPs)
	}
}

func TestUpdateAllowedSourceIPs_InvalidIP(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-iprestrict", DisplayName: "IP Restrict", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: "not-valid-ip"})
	r := newChiRequestWithBody("PUT", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)}, body)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid IP, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestUpdateAllowedSourceIPs_ClearRestrictions(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:             "bot-iprestrict",
		DisplayName:      "IP Restrict",
		Status:           "running",
		AllowedSourceIPs: "10.0.0.1",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: ""})
	r := newChiRequestWithBody("PUT", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)}, body)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp ipRestrictResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.AllowedIPs != "" {
		t.Errorf("expected empty allowed_ips, got %q", resp.AllowedIPs)
	}

	var updated database.Instance
	database.DB.First(&updated, inst.ID)
	if updated.AllowedSourceIPs != "" {
		t.Errorf("expected DB to have empty string, got %q", updated.AllowedSourceIPs)
	}
}

func TestUpdateAllowedSourceIPs_NormalizesInput(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-iprestrict", DisplayName: "IP Restrict", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: "10.0.0.5/24"})
	r := newChiRequestWithBody("PUT", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)}, body)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp ipRestrictResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.AllowedIPs != "10.0.0.0/24" {
		t.Errorf("expected normalized '10.0.0.0/24', got %q", resp.AllowedIPs)
	}
}

func TestUpdateAllowedSourceIPs_IPv6(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-iprestrict", DisplayName: "IP Restrict", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	body, _ := json.Marshal(ipRestrictUpdateRequest{AllowedIPs: "2001:db8::1, 2001:db8::/32"})
	r := newChiRequestWithBody("PUT", fmt.Sprintf("/api/v1/instances/%d/ssh-allowed-ips", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)}, body)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	UpdateAllowedSourceIPs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp ipRestrictResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.AllowedIPs == "" {
		t.Error("expected non-empty allowed_ips for IPv6")
	}
}
