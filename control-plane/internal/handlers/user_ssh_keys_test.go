package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/ssh"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

func setupSSHKeyTest(t *testing.T) *database.User {
	t.Helper()
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.UserSSHKey{}); err != nil {
		t.Fatalf("migrate UserSSHKey: %v", err)
	}
	user := &database.User{Username: "alice", PasswordHash: "x", Role: "user"}
	if err := database.CreateUser(user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func asUser(req *http.Request, user *database.User) *http.Request {
	return req.WithContext(middleware.WithUser(req.Context(), user))
}

func TestGenerateUserSSHKey(t *testing.T) {
	user := setupSSHKeyTest(t)

	w := httptest.NewRecorder()
	GenerateUserSSHKey(w, asUser(postJSON("/api/v1/auth/ssh-keys/generate", map[string]string{"name": "laptop"}), user))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Key        userSSHKeyResponse `json:"key"`
		PrivateKey string             `json:"private_key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.PrivateKey, "PRIVATE KEY") {
		t.Error("expected PEM private key in response")
	}
	if resp.Key.Name != "laptop" || !strings.HasPrefix(resp.Key.Fingerprint, "SHA256:") {
		t.Errorf("unexpected key metadata: %+v", resp.Key)
	}

	// The private key must be usable and match the stored public key.
	signer, err := ssh.ParsePrivateKey([]byte(resp.PrivateKey))
	if err != nil {
		t.Fatalf("returned private key does not parse: %v", err)
	}
	stored, err := database.GetUserSSHKeyByFingerprint(ssh.FingerprintSHA256(signer.PublicKey()))
	if err != nil {
		t.Fatalf("stored key not found by fingerprint: %v", err)
	}
	if stored.UserID != user.ID {
		t.Errorf("stored key user = %d, want %d", stored.UserID, user.ID)
	}
	// The private key must never be stored.
	if strings.Contains(stored.PublicKey, "PRIVATE") {
		t.Error("private key material stored in DB")
	}
}

func TestUploadUserSSHKey(t *testing.T) {
	user := setupSSHKeyTest(t)

	pubKey, _, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	w := httptest.NewRecorder()
	UploadUserSSHKey(w, asUser(postJSON("/api/v1/auth/ssh-keys", map[string]string{
		"name": "mykey", "public_key": string(pubKey),
	}), user))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}

	// Duplicate upload → 409.
	w = httptest.NewRecorder()
	UploadUserSSHKey(w, asUser(postJSON("/api/v1/auth/ssh-keys", map[string]string{
		"public_key": string(pubKey),
	}), user))
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409: %s", w.Code, w.Body.String())
	}

	// Garbage → 400.
	w = httptest.NewRecorder()
	UploadUserSSHKey(w, asUser(postJSON("/api/v1/auth/ssh-keys", map[string]string{
		"public_key": "not a key",
	}), user))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid key status = %d, want 400", w.Code)
	}
}

func TestListUserSSHKeys(t *testing.T) {
	user := setupSSHKeyTest(t)

	w := httptest.NewRecorder()
	GenerateUserSSHKey(w, asUser(postJSON("/api/v1/auth/ssh-keys/generate", nil), user))

	w = httptest.NewRecorder()
	ListUserSSHKeys(w, asUser(httptest.NewRequest("GET", "/api/v1/auth/ssh-keys", nil), user))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var keys []userSSHKeyResponse
	json.Unmarshal(w.Body.Bytes(), &keys)
	if len(keys) != 1 {
		t.Fatalf("listed %d keys, want 1", len(keys))
	}
}

func deleteKeyRequest(keyID uint) *http.Request {
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/auth/ssh-keys/%d", keyID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("keyId", fmt.Sprintf("%d", keyID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestDeleteUserSSHKey(t *testing.T) {
	user := setupSSHKeyTest(t)

	key := &database.UserSSHKey{UserID: user.ID, PublicKey: "k", Fingerprint: "SHA256:abc"}
	if err := database.CreateUserSSHKey(key); err != nil {
		t.Fatalf("create key: %v", err)
	}

	// Another user cannot delete it.
	other := &database.User{Username: "bob", PasswordHash: "x", Role: "user"}
	database.CreateUser(other)
	w := httptest.NewRecorder()
	DeleteUserSSHKey(w, asUser(deleteKeyRequest(key.ID), other))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete status = %d, want 404", w.Code)
	}

	// Owner can.
	w = httptest.NewRecorder()
	DeleteUserSSHKey(w, asUser(deleteKeyRequest(key.ID), user))
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner delete status = %d, want 204", w.Code)
	}
	if keys, _ := database.ListUserSSHKeys(user.ID); len(keys) != 0 {
		t.Errorf("key still present after delete")
	}
}

func TestGetSSHGatewayInfo(t *testing.T) {
	w := httptest.NewRecorder()
	GetSSHGatewayInfo(w, httptest.NewRequest("GET", "/api/v1/ssh-gateway/info", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var info map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &info)
	for _, field := range []string{"enabled", "port", "host"} {
		if _, ok := info[field]; !ok {
			t.Errorf("missing field %q in %v", field, info)
		}
	}
}
