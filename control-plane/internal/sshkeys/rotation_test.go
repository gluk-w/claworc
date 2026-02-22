package sshkeys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// --- Mock orchestrator ---

type mockAddr struct {
	host string
	port int
}

type mockExecResult struct {
	stderr   string
	exitCode int
	err      error
}

type mockOrch struct {
	mu sync.Mutex

	// Tracking
	execCalls   []mockExecCall
	configCalls []mockConfigCall

	// Configuration
	addresses      map[uint]mockAddr
	execResults    map[string]mockExecResult // keyed by instance name
	configErr      error
	addressErr     error
	addressErrByID map[uint]error
}

type mockExecCall struct {
	Name string
	Cmd  []string
}

type mockConfigCall struct {
	InstanceID uint
	PublicKey  string
}

func newMockOrch() *mockOrch {
	return &mockOrch{
		addresses:      make(map[uint]mockAddr),
		execResults:    make(map[string]mockExecResult),
		addressErrByID: make(map[uint]error),
	}
}

func (m *mockOrch) ExecInInstance(_ context.Context, name string, cmd []string) (string, string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.execCalls = append(m.execCalls, mockExecCall{Name: name, Cmd: cmd})

	if result, ok := m.execResults[name]; ok {
		return "", result.stderr, result.exitCode, result.err
	}
	return "", "", 0, nil
}

func (m *mockOrch) ConfigureSSHAccess(_ context.Context, instanceID uint, publicKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configCalls = append(m.configCalls, mockConfigCall{InstanceID: instanceID, PublicKey: publicKey})
	return m.configErr
}

func (m *mockOrch) GetSSHAddress(_ context.Context, instanceID uint) (string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err, ok := m.addressErrByID[instanceID]; ok {
		return "", 0, err
	}
	if m.addressErr != nil {
		return "", 0, m.addressErr
	}
	if addr, ok := m.addresses[instanceID]; ok {
		return addr.host, addr.port, nil
	}
	return "127.0.0.1", 22, nil
}

func (m *mockOrch) getExecCalls() []mockExecCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls := make([]mockExecCall, len(m.execCalls))
	copy(calls, m.execCalls)
	return calls
}

func (m *mockOrch) getConfigCalls() []mockConfigCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls := make([]mockConfigCall, len(m.configCalls))
	copy(calls, m.configCalls)
	return calls
}

// --- Test helpers ---

// setupKeyDir creates a temp dir with an existing key pair.
func setupKeyDir(t *testing.T) (string, ssh.Signer, string) {
	t.Helper()
	dir := t.TempDir()

	pubKey, privKey, err := sshproxy.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	if err := sshproxy.SaveKeyPair(dir, privKey, pubKey); err != nil {
		t.Fatalf("save key pair: %v", err)
	}

	signer, err := sshproxy.ParsePrivateKey(privKey)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}

	return dir, signer, string(pubKey)
}

// withTestConnection overrides testConnectionFunc for the duration of the test
// and restores it when done.
func withTestConnection(t *testing.T, fn func(ctx context.Context, signer ssh.Signer, host string, port int) error) {
	t.Helper()
	old := testConnectionFunc
	testConnectionFunc = fn
	t.Cleanup(func() { testConnectionFunc = old })
}

// --- Tests ---

func TestRotateGlobalKeyPair_FullFlow(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	// Mock: all SSH tests succeed
	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	instances := []InstanceInfo{
		{ID: 1, Name: "bot-alpha"},
		{ID: 2, Name: "bot-beta"},
	}

	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}
	orch.addresses[2] = mockAddr{host: "10.0.0.2", port: 22}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// Verify result
	if result.OldFingerprint == "" {
		t.Error("OldFingerprint is empty")
	}
	if result.NewFingerprint == "" {
		t.Error("NewFingerprint is empty")
	}
	if result.OldFingerprint == result.NewFingerprint {
		t.Error("OldFingerprint == NewFingerprint, keys were not rotated")
	}
	if !result.FullSuccess {
		t.Error("FullSuccess should be true")
	}
	if result.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}

	// Verify per-instance results
	if len(result.InstanceStatuses) != 2 {
		t.Fatalf("expected 2 instance statuses, got %d", len(result.InstanceStatuses))
	}
	for _, status := range result.InstanceStatuses {
		if !status.Success {
			t.Errorf("instance %s (ID %d) not successful: %s", status.Name, status.InstanceID, status.Error)
		}
	}

	// Verify new keys on disk
	newPrivKey, err := sshproxy.LoadPrivateKey(dir)
	if err != nil {
		t.Fatalf("load new private key: %v", err)
	}
	newPubKey, err := sshproxy.LoadPublicKey(dir)
	if err != nil {
		t.Fatalf("load new public key: %v", err)
	}
	if newPubKey == oldPubKey {
		t.Error("public key on disk was not changed")
	}

	// Verify new signer parses correctly
	newSigner, err := sshproxy.ParsePrivateKey(newPrivKey)
	if err != nil {
		t.Fatalf("parse new private key: %v", err)
	}
	if ssh.FingerprintSHA256(newSigner.PublicKey()) != result.NewFingerprint {
		t.Error("new key on disk doesn't match result fingerprint")
	}

	// Verify SSHManager was reloaded
	if sshMgr.GetPublicKeyFingerprint() != result.NewFingerprint {
		t.Error("SSHManager fingerprint not updated")
	}
	if sshMgr.GetPublicKey() != newPubKey {
		t.Error("SSHManager public key not updated")
	}

	// Verify backup files were removed (full success)
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.old")); !os.IsNotExist(err) {
		t.Error("backup private key should have been removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.pub.old")); !os.IsNotExist(err) {
		t.Error("backup public key should have been removed")
	}

	// Verify orchestrator calls
	execCalls := orch.getExecCalls()
	if len(execCalls) != 2 {
		t.Fatalf("expected 2 exec calls (append key), got %d", len(execCalls))
	}
	for _, call := range execCalls {
		if !strings.Contains(call.Cmd[len(call.Cmd)-1], ">> /root/.ssh/authorized_keys") {
			t.Errorf("exec call should append to authorized_keys: %v", call.Cmd)
		}
	}

	configCalls := orch.getConfigCalls()
	if len(configCalls) != 2 {
		t.Fatalf("expected 2 config calls (finalize key), got %d", len(configCalls))
	}
	for _, call := range configCalls {
		if call.PublicKey != newPubKey {
			t.Error("ConfigureSSHAccess should be called with new public key")
		}
	}
}

func TestRotateGlobalKeyPair_NoInstances(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	orch := newMockOrch()
	result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if !result.FullSuccess {
		t.Error("FullSuccess should be true with no instances")
	}
	if result.OldFingerprint == result.NewFingerprint {
		t.Error("keys should have been rotated")
	}

	// New keys should be on disk
	newPubKey, err := sshproxy.LoadPublicKey(dir)
	if err != nil {
		t.Fatalf("load public key: %v", err)
	}
	if newPubKey == oldPubKey {
		t.Error("public key on disk unchanged")
	}

	// SSHManager should have new keys
	if sshMgr.GetPublicKeyFingerprint() != result.NewFingerprint {
		t.Error("SSHManager not updated")
	}

	// Backups should be cleaned up
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.old")); !os.IsNotExist(err) {
		t.Error("backup files should be removed")
	}
}

func TestRotateGlobalKeyPair_PartialFailure_AppendFails(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	instances := []InstanceInfo{
		{ID: 1, Name: "bot-good"},
		{ID: 2, Name: "bot-bad"},
	}

	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}
	orch.addresses[2] = mockAddr{host: "10.0.0.2", port: 22}
	// Make exec fail for bot-bad
	orch.execResults["bot-bad"] = mockExecResult{stderr: "connection refused", exitCode: 1}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if result.FullSuccess {
		t.Error("FullSuccess should be false with partial failure")
	}

	// bot-good should succeed
	if !result.InstanceStatuses[0].Success {
		t.Errorf("bot-good should succeed: %s", result.InstanceStatuses[0].Error)
	}

	// bot-bad should fail
	if result.InstanceStatuses[1].Success {
		t.Error("bot-bad should fail")
	}
	if result.InstanceStatuses[1].Error == "" {
		t.Error("bot-bad should have error message")
	}

	// Backup files should be retained (partial failure)
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.old")); os.IsNotExist(err) {
		t.Error("backup private key should be retained on partial failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.pub.old")); os.IsNotExist(err) {
		t.Error("backup public key should be retained on partial failure")
	}
}

func TestRotateGlobalKeyPair_PartialFailure_ConnectionTestFails(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	// Make connection test fail for specific addresses
	withTestConnection(t, func(_ context.Context, _ ssh.Signer, host string, _ int) error {
		if host == "10.0.0.2" {
			return fmt.Errorf("connection refused")
		}
		return nil
	})

	instances := []InstanceInfo{
		{ID: 1, Name: "bot-good"},
		{ID: 2, Name: "bot-bad"},
	}

	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}
	orch.addresses[2] = mockAddr{host: "10.0.0.2", port: 22}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if result.FullSuccess {
		t.Error("FullSuccess should be false")
	}

	// bot-good: append succeeded, test succeeded
	if !result.InstanceStatuses[0].Success {
		t.Errorf("bot-good should succeed: %s", result.InstanceStatuses[0].Error)
	}

	// bot-bad: append succeeded but test failed
	if result.InstanceStatuses[1].Success {
		t.Error("bot-bad should fail")
	}
	if !strings.Contains(result.InstanceStatuses[1].Error, "connection test") {
		t.Errorf("expected connection test error, got: %s", result.InstanceStatuses[1].Error)
	}

	// ConfigureSSHAccess should only be called for bot-good (to finalize)
	configCalls := orch.getConfigCalls()
	if len(configCalls) != 1 {
		t.Fatalf("expected 1 config call, got %d", len(configCalls))
	}
	if configCalls[0].InstanceID != 1 {
		t.Error("ConfigureSSHAccess should be called for bot-good (ID 1)")
	}
}

func TestRotateGlobalKeyPair_AddressLookupFails(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	instances := []InstanceInfo{
		{ID: 1, Name: "bot-good"},
		{ID: 2, Name: "bot-noaddr"},
	}

	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}
	orch.addressErrByID[2] = errors.New("pod not found")

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if result.FullSuccess {
		t.Error("FullSuccess should be false")
	}

	if !result.InstanceStatuses[0].Success {
		t.Errorf("bot-good should succeed: %s", result.InstanceStatuses[0].Error)
	}

	if result.InstanceStatuses[1].Success {
		t.Error("bot-noaddr should fail")
	}
	if !strings.Contains(result.InstanceStatuses[1].Error, "get address") {
		t.Errorf("expected address error, got: %s", result.InstanceStatuses[1].Error)
	}
}

func TestRotateGlobalKeyPair_ConcurrentInstances(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		// Simulate some work
		time.Sleep(time.Millisecond)
		return nil
	})

	// Create many instances to exercise concurrency
	instances := make([]InstanceInfo, 20)
	orch := newMockOrch()
	for i := range instances {
		id := uint(i + 1)
		instances[i] = InstanceInfo{ID: id, Name: fmt.Sprintf("bot-inst-%d", id)}
		orch.addresses[id] = mockAddr{host: fmt.Sprintf("10.0.0.%d", id), port: 22}
	}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if !result.FullSuccess {
		t.Error("FullSuccess should be true")
	}
	if len(result.InstanceStatuses) != 20 {
		t.Errorf("expected 20 statuses, got %d", len(result.InstanceStatuses))
	}
	for _, status := range result.InstanceStatuses {
		if !status.Success {
			t.Errorf("instance %s failed: %s", status.Name, status.Error)
		}
	}

	// All instances should have had exec calls (append) and config calls (finalize)
	execCalls := orch.getExecCalls()
	if len(execCalls) != 20 {
		t.Errorf("expected 20 exec calls, got %d", len(execCalls))
	}
	configCalls := orch.getConfigCalls()
	if len(configCalls) != 20 {
		t.Errorf("expected 20 config calls, got %d", len(configCalls))
	}
}

func TestRotateGlobalKeyPair_BackupCreatedAndCleaned(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	// Read old keys from disk before rotation
	oldPrivKeyOnDisk, _ := sshproxy.LoadPrivateKey(dir)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()
	result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// With no instances, full success → backups cleaned
	if !result.FullSuccess {
		t.Fatal("expected full success")
	}
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.old")); !os.IsNotExist(err) {
		t.Error("backup should be removed on full success")
	}

	// New keys should differ from old
	newPrivKeyOnDisk, _ := sshproxy.LoadPrivateKey(dir)
	if bytes.Equal(oldPrivKeyOnDisk, newPrivKeyOnDisk) {
		t.Error("private key on disk unchanged after rotation")
	}
}

func TestRotateGlobalKeyPair_BackupRetainedOnPartialFailure(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	// Make all connection tests fail
	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return fmt.Errorf("refused")
	})

	instances := []InstanceInfo{{ID: 1, Name: "bot-fail"}}
	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}

	result, err := RotateGlobalKeyPair(context.Background(), dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	if result.FullSuccess {
		t.Error("should not be full success")
	}

	// Backup files should exist
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.old")); os.IsNotExist(err) {
		t.Error("backup private key should be retained")
	}
	if _, err := os.Stat(filepath.Join(dir, "ssh_key.pub.old")); os.IsNotExist(err) {
		t.Error("backup public key should be retained")
	}
}

func TestRotateGlobalKeyPair_KeyReloaded(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	oldFingerprint := sshMgr.GetPublicKeyFingerprint()

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()
	result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// SSHManager should report the new fingerprint
	newFingerprint := sshMgr.GetPublicKeyFingerprint()
	if newFingerprint == oldFingerprint {
		t.Error("SSHManager fingerprint unchanged after rotation")
	}
	if newFingerprint != result.NewFingerprint {
		t.Error("SSHManager fingerprint doesn't match result")
	}

	// SSHManager public key should be updated
	newPubKey := sshMgr.GetPublicKey()
	if newPubKey == oldPubKey {
		t.Error("SSHManager public key unchanged after rotation")
	}
}

func TestRotateGlobalKeyPair_ContextCancellation(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	// Make connection test block until context is cancelled
	withTestConnection(t, func(ctx context.Context, _ ssh.Signer, _ string, _ int) error {
		<-ctx.Done()
		return ctx.Err()
	})

	instances := []InstanceInfo{{ID: 1, Name: "bot-slow"}}
	orch := newMockOrch()
	orch.addresses[1] = mockAddr{host: "10.0.0.1", port: 22}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := RotateGlobalKeyPair(ctx, dir, instances, orch, sshMgr)
	if err != nil {
		t.Fatalf("RotateGlobalKeyPair() error: %v", err)
	}

	// Should complete but with failed instance
	if result.FullSuccess {
		t.Error("should not be full success")
	}
	if result.InstanceStatuses[0].Success {
		t.Error("timed out instance should not succeed")
	}
}

func TestAppendPublicKey(t *testing.T) {
	orch := newMockOrch()
	pubKey := "ssh-ed25519 AAAA... test@key\n"

	err := appendPublicKey(context.Background(), orch, "bot-test", pubKey)
	if err != nil {
		t.Fatalf("appendPublicKey() error: %v", err)
	}

	calls := orch.getExecCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(calls))
	}
	if calls[0].Name != "bot-test" {
		t.Errorf("exec call name: got %s, want bot-test", calls[0].Name)
	}
	cmd := calls[0].Cmd[len(calls[0].Cmd)-1]
	if !strings.Contains(cmd, ">> /root/.ssh/authorized_keys") {
		t.Errorf("command should append to authorized_keys: %s", cmd)
	}
	if !strings.Contains(cmd, "base64 -d") {
		t.Errorf("command should use base64 encoding: %s", cmd)
	}
}

func TestAppendPublicKey_ExecError(t *testing.T) {
	orch := newMockOrch()
	orch.execResults["bot-fail"] = mockExecResult{err: errors.New("container not running")}

	err := appendPublicKey(context.Background(), orch, "bot-fail", "key\n")
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "exec") {
		t.Errorf("error should mention exec: %v", err)
	}
}

func TestAppendPublicKey_NonZeroExitCode(t *testing.T) {
	orch := newMockOrch()
	orch.execResults["bot-fail"] = mockExecResult{stderr: "permission denied", exitCode: 1}

	err := appendPublicKey(context.Background(), orch, "bot-fail", "key\n")
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "exit code") {
		t.Errorf("error should mention exit code: %v", err)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()

	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "dest")
	content := []byte("test content for copy")

	if err := os.WriteFile(src, content, 0600); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile() error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("copied content doesn't match")
	}

	// Check permissions preserved
	srcInfo, _ := os.Stat(src)
	dstInfo, _ := os.Stat(dst)
	if srcInfo.Mode().Perm() != dstInfo.Mode().Perm() {
		t.Errorf("permissions: src=%o, dst=%o", srcInfo.Mode().Perm(), dstInfo.Mode().Perm())
	}
}

func TestCopyFile_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dest"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestRotateGlobalKeyPair_NewKeyDifferentFromOld(t *testing.T) {
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

	// Verify result fingerprints differ
	if result.OldFingerprint == result.NewFingerprint {
		t.Error("old and new fingerprints should differ")
	}

	// Verify disk keys match result
	diskPubKey, _ := sshproxy.LoadPublicKey(dir)
	diskPrivKey, _ := sshproxy.LoadPrivateKey(dir)
	diskSigner, _ := sshproxy.ParsePrivateKey(diskPrivKey)

	diskFingerprint := ssh.FingerprintSHA256(diskSigner.PublicKey())
	if diskFingerprint != result.NewFingerprint {
		t.Errorf("disk fingerprint %s != result %s", diskFingerprint, result.NewFingerprint)
	}

	// Verify SSHManager key matches disk
	if sshMgr.GetPublicKey() != diskPubKey {
		t.Error("SSHManager public key doesn't match disk")
	}
}

func TestRotateGlobalKeyPair_MultipleRotations(t *testing.T) {
	dir, oldSigner, oldPubKey := setupKeyDir(t)
	sshMgr := sshproxy.NewSSHManager(oldSigner, oldPubKey)

	withTestConnection(t, func(_ context.Context, _ ssh.Signer, _ string, _ int) error {
		return nil
	})

	orch := newMockOrch()

	// Rotate multiple times
	var fingerprints []string
	fingerprints = append(fingerprints, sshMgr.GetPublicKeyFingerprint())

	for i := 0; i < 3; i++ {
		result, err := RotateGlobalKeyPair(context.Background(), dir, nil, orch, sshMgr)
		if err != nil {
			t.Fatalf("rotation %d error: %v", i, err)
		}
		if !result.FullSuccess {
			t.Errorf("rotation %d not full success", i)
		}
		fingerprints = append(fingerprints, result.NewFingerprint)
	}

	// All fingerprints should be unique
	seen := make(map[string]bool)
	for _, fp := range fingerprints {
		if seen[fp] {
			t.Errorf("duplicate fingerprint: %s", fp)
		}
		seen[fp] = true
	}
}
