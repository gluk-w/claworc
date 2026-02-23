package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// setupRotationTest sets up an in-memory DB, temp key dir, SSH manager, and mock orchestrator.
func setupRotationTest(t *testing.T) (keyDir string, cleanup func()) {
	t.Helper()
	setupTestDB(t)

	// Create temp dir for SSH keys
	keyDir = t.TempDir()

	// Generate initial key pair
	signer, pubKey, err := sshproxy.EnsureKeyPair(keyDir)
	if err != nil {
		t.Fatalf("ensure key pair: %v", err)
	}

	// Set up SSH manager as package var
	mgr := sshproxy.NewSSHManager(signer, pubKey)
	SSHMgr = mgr

	// Set up config data path
	config.Cfg.DataPath = keyDir

	// Set up mock orchestrator
	mock := &mockOrchestrator{sshHost: "127.0.0.1", sshPort: 22}
	orchestrator.Set(mock)

	cleanup = func() {
		SSHMgr = nil
		orchestrator.Set(nil)
	}
	return keyDir, cleanup
}

// --- RotateSSHKey handler tests ---

func TestRotateSSHKey_Success(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	// Override the sshkeys test connection func to always succeed
	origTestConn := sshkeys.GetTestConnectionFunc()
	sshkeys.SetTestConnectionFunc(func(ctx context.Context, signer interface{}, host string, port int) error {
		return nil
	})
	defer sshkeys.SetTestConnectionFunc(origTestConn)

	// Create running instances
	createTestInstance(t, "bot-test1", "Test 1")
	createTestInstance(t, "bot-test2", "Test 2")

	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/settings/rotate-ssh-key", user, nil)
	w := httptest.NewRecorder()

	RotateSSHKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result sshkeys.RotationResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if result.OldFingerprint == "" {
		t.Error("expected old_fingerprint to be set")
	}
	if result.NewFingerprint == "" {
		t.Error("expected new_fingerprint to be set")
	}
	if result.OldFingerprint == result.NewFingerprint {
		t.Error("expected different fingerprints after rotation")
	}
	if result.Timestamp.IsZero() {
		t.Error("expected timestamp to be set")
	}

	// Verify last_rotation setting was recorded
	lastRotation, err := database.GetSetting("ssh_key_last_rotation")
	if err != nil {
		t.Fatalf("get last_rotation: %v", err)
	}
	if lastRotation == "" {
		t.Error("expected ssh_key_last_rotation to be set")
	}
	ts, err := time.Parse(time.RFC3339, lastRotation)
	if err != nil {
		t.Fatalf("parse last_rotation: %v", err)
	}
	if time.Since(ts) > time.Minute {
		t.Error("expected last_rotation to be recent")
	}
}

func TestRotateSSHKey_NoSSHManager(t *testing.T) {
	setupTestDB(t)
	SSHMgr = nil

	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/settings/rotate-ssh-key", user, nil)
	w := httptest.NewRecorder()

	RotateSSHKey(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRotateSSHKey_NoOrchestrator(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	// Clear orchestrator
	orchestrator.Set(nil)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/settings/rotate-ssh-key", user, nil)
	w := httptest.NewRecorder()

	RotateSSHKey(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRotateSSHKey_NoRunningInstances(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	// Override test connection func (shouldn't be called)
	origTestConn := sshkeys.GetTestConnectionFunc()
	sshkeys.SetTestConnectionFunc(func(ctx context.Context, signer interface{}, host string, port int) error {
		t.Error("test connection should not be called with no instances")
		return nil
	})
	defer sshkeys.SetTestConnectionFunc(origTestConn)

	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/settings/rotate-ssh-key", user, nil)
	w := httptest.NewRecorder()

	RotateSSHKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRotateSSHKey_KeyFilesUpdated(t *testing.T) {
	keyDir, cleanup := setupRotationTest(t)
	defer cleanup()

	origTestConn := sshkeys.GetTestConnectionFunc()
	sshkeys.SetTestConnectionFunc(func(ctx context.Context, signer interface{}, host string, port int) error {
		return nil
	})
	defer sshkeys.SetTestConnectionFunc(origTestConn)

	// Read old key file contents
	oldPriv, err := os.ReadFile(filepath.Join(keyDir, "ssh_key"))
	if err != nil {
		t.Fatalf("read old private key: %v", err)
	}
	oldPub, err := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))
	if err != nil {
		t.Fatalf("read old public key: %v", err)
	}

	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/settings/rotate-ssh-key", user, nil)
	w := httptest.NewRecorder()

	RotateSSHKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify key files were changed
	newPriv, err := os.ReadFile(filepath.Join(keyDir, "ssh_key"))
	if err != nil {
		t.Fatalf("read new private key: %v", err)
	}
	newPub, err := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))
	if err != nil {
		t.Fatalf("read new public key: %v", err)
	}

	if string(oldPriv) == string(newPriv) {
		t.Error("expected private key to change after rotation")
	}
	if string(oldPub) == string(newPub) {
		t.Error("expected public key to change after rotation")
	}
}

// --- Background job tests ---

func TestCheckAndRotateKeys_RotatesWhenDue(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	origTestConn := sshkeys.GetTestConnectionFunc()
	sshkeys.SetTestConnectionFunc(func(ctx context.Context, signer interface{}, host string, port int) error {
		return nil
	})
	defer sshkeys.SetTestConnectionFunc(origTestConn)

	// Set policy to 1 day
	database.SetSetting("ssh_key_rotation_policy_days", "1")

	// Set last rotation to 2 days ago (past due)
	database.SetSetting("ssh_key_last_rotation", time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339))

	createTestInstance(t, "bot-test", "Test")

	// Run the check
	checkAndRotateKeys(context.Background())

	// Verify rotation happened by checking the timestamp was updated
	lastRotation, err := database.GetSetting("ssh_key_last_rotation")
	if err != nil {
		t.Fatalf("get last_rotation: %v", err)
	}
	ts, err := time.Parse(time.RFC3339, lastRotation)
	if err != nil {
		t.Fatalf("parse last_rotation: %v", err)
	}
	if time.Since(ts) > time.Minute {
		t.Error("expected last_rotation to be updated to recent time after auto-rotation")
	}
}

func TestCheckAndRotateKeys_SkipsWhenNotDue(t *testing.T) {
	keyDir, cleanup := setupRotationTest(t)
	defer cleanup()

	// Set policy to 90 days
	database.SetSetting("ssh_key_rotation_policy_days", "90")

	// Set last rotation to 1 hour ago (not due)
	database.SetSetting("ssh_key_last_rotation", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))

	// Read current key
	oldPub, err := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}

	// Run the check
	checkAndRotateKeys(context.Background())

	// Verify key was NOT rotated
	newPub, err := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	if string(oldPub) != string(newPub) {
		t.Error("expected key to remain unchanged when not due for rotation")
	}
}

func TestCheckAndRotateKeys_RotatesWhenNeverRotated(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	origTestConn := sshkeys.GetTestConnectionFunc()
	sshkeys.SetTestConnectionFunc(func(ctx context.Context, signer interface{}, host string, port int) error {
		return nil
	})
	defer sshkeys.SetTestConnectionFunc(origTestConn)

	// Set policy to 1 day (any non-zero value)
	database.SetSetting("ssh_key_rotation_policy_days", "1")

	// Don't set ssh_key_last_rotation — simulates never rotated
	// The zero time.Time will be older than any policy threshold

	// Run the check
	checkAndRotateKeys(context.Background())

	// Verify rotation happened
	lastRotation, err := database.GetSetting("ssh_key_last_rotation")
	if err != nil {
		t.Fatalf("get last_rotation: %v", err)
	}
	if lastRotation == "" {
		t.Error("expected ssh_key_last_rotation to be set after auto-rotation")
	}
}

func TestCheckAndRotateKeys_NoSSHManager(t *testing.T) {
	setupTestDB(t)
	SSHMgr = nil
	// Should not panic
	checkAndRotateKeys(context.Background())
}

func TestCheckAndRotateKeys_NoOrchestrator(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()
	orchestrator.Set(nil)

	database.SetSetting("ssh_key_rotation_policy_days", "1")

	// Should not panic, just log and return
	checkAndRotateKeys(context.Background())
}

func TestStartKeyRotationJob_CancelStopsJob(t *testing.T) {
	ctx := context.Background()
	cancel := StartKeyRotationJob(ctx)

	// Should be able to cancel without panic
	cancel()
}

// --- Rotation policy settings tests ---

func TestRotateSSHKey_ResponseContainsInstanceStatuses(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	origTestConn := sshkeys.GetTestConnectionFunc()
	sshkeys.SetTestConnectionFunc(func(ctx context.Context, signer interface{}, host string, port int) error {
		return nil
	})
	defer sshkeys.SetTestConnectionFunc(origTestConn)

	createTestInstance(t, "bot-alpha", "Alpha")
	createTestInstance(t, "bot-beta", "Beta")

	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/settings/rotate-ssh-key", user, nil)
	w := httptest.NewRecorder()

	RotateSSHKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	statuses, ok := result["instance_statuses"].([]interface{})
	if !ok {
		t.Fatal("expected instance_statuses array in response")
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 instance statuses, got %d", len(statuses))
	}

	for _, s := range statuses {
		status := s.(map[string]interface{})
		if _, ok := status["instance_id"]; !ok {
			t.Error("expected instance_id in status")
		}
		if _, ok := status["name"]; !ok {
			t.Error("expected name in status")
		}
		if _, ok := status["success"]; !ok {
			t.Error("expected success in status")
		}
	}
}

func TestCheckAndRotateKeys_InvalidPolicyDays(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	keyDir := config.Cfg.DataPath
	oldPub, _ := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))

	// Set invalid policy
	database.SetSetting("ssh_key_rotation_policy_days", "notanumber")

	checkAndRotateKeys(context.Background())

	// Key should not change
	newPub, _ := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))
	if string(oldPub) != string(newPub) {
		t.Error("expected key to remain unchanged with invalid policy")
	}
}

func TestCheckAndRotateKeys_ZeroPolicyDays(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	keyDir := config.Cfg.DataPath
	oldPub, _ := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))

	// Set zero policy (should skip)
	database.SetSetting("ssh_key_rotation_policy_days", "0")

	checkAndRotateKeys(context.Background())

	// Key should not change
	newPub, _ := os.ReadFile(filepath.Join(keyDir, "ssh_key.pub"))
	if string(oldPub) != string(newPub) {
		t.Error("expected key to remain unchanged with zero policy")
	}
}

func TestRotateSSHKey_PartialFailure(t *testing.T) {
	_, cleanup := setupRotationTest(t)
	defer cleanup()

	origTestConn := sshkeys.GetTestConnectionFunc()
	callCount := 0
	sshkeys.SetTestConnectionFunc(func(ctx context.Context, signer interface{}, host string, port int) error {
		callCount++
		if callCount == 2 {
			return fmt.Errorf("connection refused")
		}
		return nil
	})
	defer sshkeys.SetTestConnectionFunc(origTestConn)

	createTestInstance(t, "bot-ok", "OK")
	createTestInstance(t, "bot-fail", "Fail")

	user := createTestUser(t, "admin")
	req := buildRequest(t, "POST", "/api/v1/settings/rotate-ssh-key", user, nil)
	w := httptest.NewRecorder()

	RotateSSHKey(w, req)

	// Should still succeed (partial failure is not an error)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result sshkeys.RotationResult
	json.NewDecoder(w.Body).Decode(&result)

	if result.FullSuccess {
		t.Error("expected partial failure (full_success=false)")
	}
}
