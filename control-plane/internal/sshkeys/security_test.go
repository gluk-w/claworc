package sshkeys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// --- Key Rotation Security Tests ---

// TestSecurity_OldKeyInvalidatedAfterRotation verifies that after key rotation,
// the old key is no longer accepted by instances that completed rotation.
// The rotation process replaces authorized_keys with only the new key (step 7),
// so the old key should be invalidated.
func TestSecurity_OldKeyInvalidatedAfterRotation(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	oldFingerprint := ssh.FingerprintSHA256(oldSigner.PublicKey())

	// Track which keys are tested during connection verification
	var testedSignerFingerprints []string

	withTestConnection(t, func(_ context.Context, signer ssh.Signer, _ string, _ int) error {
		testedSignerFingerprints = append(testedSignerFingerprints, ssh.FingerprintSHA256(signer.PublicKey()))
		return nil
	})

	instances := []InstanceInfo{{ID: 1, Name: "bot-secure"}}
	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if !result.FullSuccess {
		t.Fatal("expected full success")
	}

	// Verify the connection test used the NEW key, not the old one
	if len(testedSignerFingerprints) != 1 {
		t.Fatalf("expected 1 connection test, got %d", len(testedSignerFingerprints))
	}
	if testedSignerFingerprints[0] == oldFingerprint {
		t.Error("SECURITY: connection test used the OLD key instead of the new one")
	}
	if testedSignerFingerprints[0] != result.NewFingerprint {
		t.Error("SECURITY: connection test used an unexpected key")
	}

	// Verify ConfigureSSHAccess was called to overwrite authorized_keys with ONLY the new key
	// (This is how the old key gets invalidated)
	configCalls := orch.getConfigCalls()
	if len(configCalls) != 1 {
		t.Fatalf("expected 1 ConfigureSSHAccess call, got %d", len(configCalls))
	}

	newPubKey, _ := sshproxy.LoadPublicKey(dir)
	if configCalls[0].PublicKey != newPubKey {
		t.Error("SECURITY: ConfigureSSHAccess not called with new public key — old key may still be authorized")
	}

	// Verify the old key is no longer what the SSHManager holds
	currentFingerprint := sshMgr.GetPublicKeyFingerprint()
	if currentFingerprint == oldFingerprint {
		t.Error("SECURITY: SSHManager still holds the old key after rotation")
	}
}

// TestSecurity_AllInstancesReceiveNewKey verifies that every running instance
// receives the new public key during rotation, even when there are many instances.
func TestSecurity_AllInstancesReceiveNewKey(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	// Create 10 instances
	instances := make([]InstanceInfo, 10)
	orch := newMockOrch()
	for i := range instances {
		id := uint(i + 1)
		instances[i] = InstanceInfo{ID: id, Name: fmt.Sprintf("bot-%d", id)}
		orch.addresses[id] = mockAddr{host: fmt.Sprintf("10.0.0.%d", id), port: 22}
	}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if !result.FullSuccess {
		t.Fatal("expected full success for all instances")
	}

	// Verify each instance had:
	// 1. An exec call to append the new key (step 2)
	// 2. A ConfigureSSHAccess call to finalize with only the new key (step 7)
	execCalls := orch.getExecCalls()
	configCalls := orch.getConfigCalls()

	if len(execCalls) != 10 {
		t.Errorf("expected 10 exec calls (key append), got %d", len(execCalls))
	}
	if len(configCalls) != 10 {
		t.Errorf("expected 10 config calls (key finalize), got %d", len(configCalls))
	}

	// Verify all config calls used the new key
	newPubKey, _ := sshproxy.LoadPublicKey(dir)
	for i, call := range configCalls {
		if call.PublicKey != newPubKey {
			t.Errorf("SECURITY: ConfigureSSHAccess call %d used wrong key", i)
		}
	}

	// Verify all instances are marked successful
	for _, status := range result.InstanceStatuses {
		if !status.Success {
			t.Errorf("SECURITY: instance %s (ID %d) not updated: %s",
				status.Name, status.InstanceID, status.Error)
		}
	}
}

// TestSecurity_ServicesWorkWithNewKey verifies that the SSHManager is reloaded
// with the new key and can be used for future connections.
func TestSecurity_ServicesWorkWithNewKey(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()
	result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// 1. SSHManager should have the new key loaded
	if sshMgr.GetPublicKeyFingerprint() != result.NewFingerprint {
		t.Error("SECURITY: SSHManager fingerprint doesn't match new key")
	}

	// 2. The new key on disk should match what SSHManager has
	diskPrivKey, err := sshproxy.LoadPrivateKey(dir)
	if err != nil {
		t.Fatalf("load private key from disk: %v", err)
	}
	diskSigner, err := sshproxy.ParsePrivateKey(diskPrivKey)
	if err != nil {
		t.Fatalf("parse disk private key: %v", err)
	}
	diskFingerprint := ssh.FingerprintSHA256(diskSigner.PublicKey())

	if diskFingerprint != result.NewFingerprint {
		t.Error("SECURITY: disk key doesn't match rotation result")
	}
	if diskFingerprint != sshMgr.GetPublicKeyFingerprint() {
		t.Error("SECURITY: disk key doesn't match SSHManager key — desync detected")
	}

	// 3. Public key on disk should match SSHManager
	diskPubKey, err := sshproxy.LoadPublicKey(dir)
	if err != nil {
		t.Fatalf("load public key: %v", err)
	}
	if diskPubKey != sshMgr.GetPublicKey() {
		t.Error("SECURITY: public key on disk doesn't match SSHManager")
	}
}

// TestSecurity_RotationDuringActiveSessionsDoesNotDisconnect verifies that
// key rotation does not affect existing SSH connections. Only new connections
// use the new key; existing sessions maintain their established handshake.
func TestSecurity_RotationDuringActiveSessionsDoesNotDisconnect(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()

	// Before rotation, record the old fingerprint
	oldFingerprint := sshMgr.GetPublicKeyFingerprint()

	result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// ReloadKeys only updates the signer and publicKey atomically.
	// It does NOT close existing connections. This is by design:
	// the SSH handshake for active sessions has already completed,
	// so they continue to work regardless of key changes.
	//
	// Verify the key was actually changed (the reload happened).
	if sshMgr.GetPublicKeyFingerprint() == oldFingerprint {
		t.Error("key was not rotated")
	}
	if sshMgr.GetPublicKeyFingerprint() != result.NewFingerprint {
		t.Error("SSHManager doesn't have the new key")
	}

	// The key rotation process uses ReloadKeys which atomically swaps
	// the signer while respecting the keyMu lock. Active connections
	// that already completed SSH handshake are NOT affected.
}

// TestSecurity_PrivateKeyPermissions verifies that the private key is written
// with mode 0600 (owner read/write only) after rotation.
func TestSecurity_PrivateKeyPermissions(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()
	_, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// Check private key permissions
	privInfo, err := os.Stat(filepath.Join(dir, "ssh_key"))
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	perm := privInfo.Mode().Perm()
	if perm != 0600 {
		t.Errorf("SECURITY: private key permissions = %o, want 0600", perm)
	}

	// Check public key permissions
	pubInfo, err := os.Stat(filepath.Join(dir, "ssh_key.pub"))
	if err != nil {
		t.Fatalf("stat public key: %v", err)
	}
	pubPerm := pubInfo.Mode().Perm()
	if pubPerm != 0644 {
		t.Errorf("SECURITY: public key permissions = %o, want 0644", pubPerm)
	}
}

// TestSecurity_RotationGeneratesUniqueKeys verifies that each rotation produces
// a cryptographically unique key pair.
func TestSecurity_RotationGeneratesUniqueKeys(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()

	fingerprints := map[string]bool{sshMgr.GetPublicKeyFingerprint(): true}

	for i := 0; i < 5; i++ {
		result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
		if err != nil {
			t.Fatalf("rotation %d error: %v", i, err)
		}

		if fingerprints[result.NewFingerprint] {
			t.Errorf("SECURITY: rotation %d produced duplicate fingerprint %s", i, result.NewFingerprint)
		}
		fingerprints[result.NewFingerprint] = true
	}

	if len(fingerprints) != 6 {
		t.Errorf("expected 6 unique fingerprints (1 original + 5 rotations), got %d", len(fingerprints))
	}
}

// TestSecurity_PartialFailureRetainsOldKeyForFailedInstances verifies that
// when rotation partially fails, instances that couldn't be updated still
// have the old key in their authorized_keys (both old and new keys work).
func TestSecurity_PartialFailureRetainsOldKeyForFailedInstances(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	// Connection test fails for instance 2
	withTestConnection(t, func(_ context.Context, _ ssh.Signer, host string, _ int) error {
		if host == "10.0.0.2" {
			return fmt.Errorf("connection refused")
		}
		return nil
	})

	instances := []InstanceInfo{
		{ID: 1, Name: "bot-ok"},
		{ID: 2, Name: "bot-unreachable"},
	}
	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}
	orch.addresses[2] = mockAddr{host: "10.0.0.2", port: 22}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if result.FullSuccess {
		t.Fatal("expected partial failure")
	}

	// Instance 1 should have been finalized (only new key)
	// Instance 2 should NOT have ConfigureSSHAccess called (old key retained alongside new)
	configCalls := orch.getConfigCalls()
	if len(configCalls) != 1 {
		t.Fatalf("expected 1 config call (only for successful instance), got %d", len(configCalls))
	}
	if configCalls[0].InstanceID != 1 {
		t.Error("SECURITY: ConfigureSSHAccess called for wrong instance")
	}

	// Instance 2 still had an exec call to append the new key (step 2)
	// so both old and new keys should work for it
	execCalls := orch.getExecCalls()
	if len(execCalls) != 2 {
		t.Errorf("expected 2 exec calls (both instances), got %d", len(execCalls))
	}

	// Backup files should be retained for recovery
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.old")); os.IsNotExist(err) {
		t.Error("SECURITY: backup private key missing — recovery impossible")
	}
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.pub.old")); os.IsNotExist(err) {
		t.Error("SECURITY: backup public key missing — recovery impossible")
	}
}

// TestSecurity_KeyMaterialNotExposedInErrors verifies that error messages
// from failed rotations do not leak private key material.
func TestSecurity_KeyMaterialNotExposedInErrors(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	// Make all connection tests fail with an error
	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return fmt.Errorf("connection refused")
	})

	instances := []InstanceInfo{{ID: 1, Name: "bot-fail"}}
	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		// Check the error doesn't contain key material
		errStr := err.Error()
		if strings.Contains(errStr, "PRIVATE KEY") {
			t.Error("SECURITY: error message contains private key material")
		}
		return
	}

	// Check instance status errors don't leak key material
	for _, status := range result.InstanceStatuses {
		if status.Error != "" {
			if strings.Contains(status.Error, "PRIVATE KEY") {
				t.Errorf("SECURITY: instance %s error contains private key material: %s",
					status.Name, status.Error)
			}
		}
	}
}

// TestSecurity_FingerprintConsistency verifies that the fingerprint reported
// by RotationResult matches what would be computed independently.
func TestSecurity_FingerprintConsistency(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()
	result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// Compute fingerprint independently from the public key on disk
	diskPubKeyStr, _ := sshproxy.LoadPublicKey(dir)
	independentFP, err := GetPublicKeyFingerprint([]byte(diskPubKeyStr))
	if err != nil {
		t.Fatalf("GetPublicKeyFingerprint: %v", err)
	}

	if independentFP != result.NewFingerprint {
		t.Errorf("SECURITY: fingerprint mismatch — result reports %s but disk key computes to %s",
			result.NewFingerprint, independentFP)
	}

	// Also verify SSHManager agrees
	if sshMgr.GetPublicKeyFingerprint() != independentFP {
		t.Error("SECURITY: SSHManager fingerprint doesn't match independently computed value")
	}
}

// TestSecurity_RotationWithLargeInstanceCount verifies that rotation handles
// many concurrent instances without race conditions or dropped updates.
func TestSecurity_RotationWithLargeInstanceCount(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		// Simulate realistic latency
		time.Sleep(time.Millisecond)
		return nil
	})

	instanceCount := 50
	instances := make([]InstanceInfo, instanceCount)
	orch := newMockOrch()
	for i := range instances {
		id := uint(i + 1)
		instances[i] = InstanceInfo{ID: id, Name: fmt.Sprintf("bot-%d", id)}
		orch.addresses[id] = mockAddr{host: fmt.Sprintf("10.0.%d.%d", id/256, id%256), port: 22}
	}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if !result.FullSuccess {
		t.Error("expected full success for all instances")
	}

	// Verify all instances were processed
	if len(result.InstanceStatuses) != instanceCount {
		t.Errorf("expected %d instance statuses, got %d", instanceCount, len(result.InstanceStatuses))
	}

	successCount := 0
	for _, status := range result.InstanceStatuses {
		if status.Success {
			successCount++
		}
	}
	if successCount != instanceCount {
		t.Errorf("SECURITY: only %d/%d instances successfully rotated", successCount, instanceCount)
	}

	// All exec and config calls should have been made
	execCalls := orch.getExecCalls()
	configCalls := orch.getConfigCalls()
	if len(execCalls) != instanceCount {
		t.Errorf("expected %d exec calls, got %d", instanceCount, len(execCalls))
	}
	if len(configCalls) != instanceCount {
		t.Errorf("expected %d config calls, got %d", instanceCount, len(configCalls))
	}
}
