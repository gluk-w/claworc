package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
)

func TestGetSSHFingerprint_InvalidID(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	r := newChiRequest("GET", "/api/v1/instances/abc/ssh-fingerprint", map[string]string{"id": "abc"})
	w := httptest.NewRecorder()

	GetSSHFingerprint(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestGetSSHFingerprint_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", "/api/v1/instances/999/ssh-fingerprint", map[string]string{"id": "999"})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetSSHFingerprint_Forbidden(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-fp-test", DisplayName: "FP Test", Status: "running"}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestGetSSHFingerprint_NoSSHKey(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-fp-test", DisplayName: "FP Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestGetSSHFingerprint_ValidKey(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// Generate a real key pair
	pubKey, _, err := sshkeys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}

	inst := database.Instance{
		Name:         "bot-fp-test",
		DisplayName:  "FP Test",
		Status:       "running",
		SSHPublicKey: string(pubKey),
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshFingerprintResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Fingerprint == "" {
		t.Error("expected non-empty fingerprint")
	}
	if resp.Algorithm == "" {
		t.Error("expected non-empty algorithm")
	}
	// ED25519 keys should have ssh-ed25519 algorithm
	if resp.Algorithm != "ssh-ed25519" {
		t.Errorf("expected algorithm 'ssh-ed25519', got %q", resp.Algorithm)
	}
	// SHA256 fingerprints start with "SHA256:"
	if len(resp.Fingerprint) < 7 || resp.Fingerprint[:7] != "SHA256:" {
		t.Errorf("expected fingerprint to start with 'SHA256:', got %q", resp.Fingerprint)
	}
}

func TestGetSSHFingerprint_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, err := sshkeys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}

	inst := database.Instance{
		Name:         "bot-fp-fmt",
		DisplayName:  "FP Format",
		Status:       "running",
		SSHPublicKey: string(pubKey),
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if _, ok := raw["fingerprint"]; !ok {
		t.Error("response missing 'fingerprint' field")
	}
	if _, ok := raw["algorithm"]; !ok {
		t.Error("response missing 'algorithm' field")
	}
	if _, ok := raw["verified"]; !ok {
		t.Error("response missing 'verified' field")
	}
}

func TestGetSSHFingerprint_ViewerAssigned(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, err := sshkeys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}

	inst := database.Instance{
		Name:         "bot-fp-viewer",
		DisplayName:  "FP Viewer",
		Status:       "running",
		SSHPublicKey: string(pubKey),
	}
	database.DB.Create(&inst)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Assign viewer to instance
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst.ID})

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for assigned viewer, got %d", w.Code)
	}
}

func TestGetSSHFingerprint_VerifiedWithStoredFingerprint(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, err := sshkeys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}

	fp, err := sshkeys.GetPublicKeyFingerprint(pubKey)
	if err != nil {
		t.Fatalf("failed to get fingerprint: %v", err)
	}

	inst := database.Instance{
		Name:              "bot-fp-verified",
		DisplayName:       "FP Verified",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHKeyFingerprint: fp,
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshFingerprintResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Verified {
		t.Error("expected verified=true when stored fingerprint matches")
	}
	if resp.Fingerprint != fp {
		t.Errorf("fingerprint mismatch: expected %q, got %q", fp, resp.Fingerprint)
	}
}

func TestGetSSHFingerprint_NotVerifiedOnMismatch(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, err := sshkeys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}

	inst := database.Instance{
		Name:              "bot-fp-mismatch",
		DisplayName:       "FP Mismatch",
		Status:            "running",
		SSHPublicKey:      string(pubKey),
		SSHKeyFingerprint: "SHA256:bogus-fingerprint-value",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshFingerprintResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Verified {
		t.Error("expected verified=false when stored fingerprint does not match")
	}
}

func TestGetSSHFingerprint_VerifiedWhenNoStoredFingerprint(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	pubKey, _, err := sshkeys.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate key pair: %v", err)
	}

	// No SSHKeyFingerprint stored (legacy instance)
	inst := database.Instance{
		Name:         "bot-fp-legacy",
		DisplayName:  "FP Legacy",
		Status:       "running",
		SSHPublicKey: string(pubKey),
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshFingerprintResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Verified {
		t.Error("expected verified=true when no stored fingerprint (legacy)")
	}
}

func TestGetSSHFingerprint_InvalidPublicKey(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{
		Name:         "bot-fp-invalid",
		DisplayName:  "FP Invalid",
		Status:       "running",
		SSHPublicKey: "not-a-valid-key",
	}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	r := newChiRequest("GET", fmt.Sprintf("/api/v1/instances/%d/ssh-fingerprint", inst.ID), map[string]string{"id": fmt.Sprint(inst.ID)})
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHFingerprint(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d body: %s", w.Code, w.Body.String())
	}
}
