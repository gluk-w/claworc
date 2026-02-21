// Package sshkeys implements SSH key rotation for the global key pair.
//
// The control plane uses a single ED25519 key pair to authenticate with all
// agent instances. Key rotation replaces this global key pair on disk and
// propagates the new public key to all running instances in a safe multi-step
// process that avoids interrupting active SSH sessions.
package sshkeys

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"golang.org/x/crypto/ssh"
)

// RotationOrchestrator defines the orchestrator methods needed for key rotation.
// The full ContainerOrchestrator satisfies this interface.
type RotationOrchestrator interface {
	ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error
	GetSSHAddress(ctx context.Context, instanceID uint) (host string, port int, err error)
	ExecInInstance(ctx context.Context, name string, cmd []string) (stdout string, stderr string, exitCode int, err error)
}

// InstanceInfo holds the minimal instance data needed for key rotation.
type InstanceInfo struct {
	ID   uint
	Name string // K8s-safe name (e.g., "bot-myinstance")
}

// RotationResult captures the outcome of a key rotation.
type RotationResult struct {
	OldFingerprint   string                   `json:"old_fingerprint"`
	NewFingerprint   string                   `json:"new_fingerprint"`
	Timestamp        time.Time                `json:"timestamp"`
	InstanceStatuses []InstanceRotationStatus  `json:"instance_statuses"`
	FullSuccess      bool                     `json:"full_success"`
}

// InstanceRotationStatus captures the per-instance outcome of a key rotation.
type InstanceRotationStatus struct {
	InstanceID uint   `json:"instance_id"`
	Name       string `json:"name"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}

// testConnectionFunc is the function used to test SSH connectivity with the new key.
// It is a package-level var so tests can override it without needing a real SSH server.
var testConnectionFunc = defaultTestConnection

func defaultTestConnection(ctx context.Context, signer ssh.Signer, host string, port int) error {
	cfg := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	_, err = session.Output("echo ping")
	return err
}

// RotateGlobalKeyPair generates a new ED25519 key pair and rotates it across
// all running instances. The rotation follows a safe multi-step process:
//
//  1. Generate a new key pair
//  2. Append new public key to each instance's authorized_keys (both keys work)
//  3. Back up old key files on disk
//  4. Write new key files to disk
//  5. Reload new key into SSHManager
//  6. Test SSH to each instance with the new key
//  7. Remove old key from authorized_keys where new key works
//  8. Remove backup files on full success
//
// Partial failures are handled gracefully: instances where the new key fails
// retain the old key in authorized_keys and are logged as warnings.
func RotateGlobalKeyPair(ctx context.Context, keyDir string, instances []InstanceInfo, orch RotationOrchestrator, sshMgr *sshproxy.SSHManager) (*RotationResult, error) {
	result := &RotationResult{
		Timestamp:        time.Now(),
		OldFingerprint:   sshMgr.GetPublicKeyFingerprint(),
		InstanceStatuses: make([]InstanceRotationStatus, len(instances)),
	}

	// Initialize instance statuses
	for i, inst := range instances {
		result.InstanceStatuses[i] = InstanceRotationStatus{
			InstanceID: inst.ID,
			Name:       inst.Name,
		}
	}

	// Step 1: Generate new ED25519 key pair
	newPubKey, newPrivKeyPEM, err := sshproxy.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generate new key pair: %w", err)
	}

	newSigner, err := sshproxy.ParsePrivateKey(newPrivKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse new private key: %w", err)
	}
	result.NewFingerprint = ssh.FingerprintSHA256(newSigner.PublicKey())

	log.Printf("SSH key rotation: generating new key (old=%s, new=%s)", result.OldFingerprint, result.NewFingerprint)

	// Step 2: Append new public key to each instance (concurrently)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, inst := range instances {
		wg.Add(1)
		go func(idx int, inst InstanceInfo) {
			defer wg.Done()
			if err := appendPublicKey(ctx, orch, inst.Name, string(newPubKey)); err != nil {
				mu.Lock()
				result.InstanceStatuses[idx].Error = fmt.Sprintf("append key: %v", err)
				mu.Unlock()
				log.Printf("SSH key rotation: failed to append new key to instance %s (ID %d): %v", inst.Name, inst.ID, err)
			}
		}(i, inst)
	}
	wg.Wait()

	// Step 3: Back up old key files
	backupPrivPath := filepath.Join(keyDir, "ssh_key.old")
	backupPubPath := filepath.Join(keyDir, "ssh_key.pub.old")

	if err := copyFile(filepath.Join(keyDir, "ssh_key"), backupPrivPath); err != nil {
		return nil, fmt.Errorf("backup private key: %w", err)
	}
	if err := copyFile(filepath.Join(keyDir, "ssh_key.pub"), backupPubPath); err != nil {
		os.Remove(backupPrivPath)
		return nil, fmt.Errorf("backup public key: %w", err)
	}

	// Step 4: Write new key pair to disk
	if err := sshproxy.SaveKeyPair(keyDir, newPrivKeyPEM, newPubKey); err != nil {
		// Restore from backup on failure
		restoreFile(backupPrivPath, filepath.Join(keyDir, "ssh_key"))
		restoreFile(backupPubPath, filepath.Join(keyDir, "ssh_key.pub"))
		return nil, fmt.Errorf("save new key pair: %w", err)
	}

	// Step 5: Reload new key into SSHManager
	sshMgr.ReloadKeys(newSigner, string(newPubKey))

	// Steps 6 & 7: Test SSH with new key and remove old key (concurrently)
	wg = sync.WaitGroup{}
	for i, inst := range instances {
		// Skip instances where the append already failed
		if result.InstanceStatuses[i].Error != "" {
			continue
		}

		wg.Add(1)
		go func(idx int, inst InstanceInfo) {
			defer wg.Done()

			host, port, err := orch.GetSSHAddress(ctx, inst.ID)
			if err != nil {
				mu.Lock()
				result.InstanceStatuses[idx].Error = fmt.Sprintf("get address: %v", err)
				mu.Unlock()
				return
			}

			if err := testConnectionFunc(ctx, newSigner, host, port); err != nil {
				mu.Lock()
				result.InstanceStatuses[idx].Error = fmt.Sprintf("connection test: %v", err)
				mu.Unlock()
				log.Printf("SSH key rotation: new key test failed for instance %s (ID %d): %v", inst.Name, inst.ID, err)
				return
			}

			// Step 7: Remove old key by overwriting authorized_keys with only new key
			if err := orch.ConfigureSSHAccess(ctx, inst.ID, string(newPubKey)); err != nil {
				log.Printf("SSH key rotation: failed to finalize key for instance %s (ID %d): %v", inst.Name, inst.ID, err)
				// Not critical — new key works, old key is also still present
			}

			mu.Lock()
			result.InstanceStatuses[idx].Success = true
			mu.Unlock()
		}(i, inst)
	}
	wg.Wait()

	// Step 8: Log warnings for failed instances
	for _, status := range result.InstanceStatuses {
		if !status.Success && status.Error != "" {
			log.Printf("SSH key rotation: instance %s (ID %d) still uses old key: %s", status.Name, status.InstanceID, status.Error)
		}
	}

	// Compute full success and clean up backups
	result.FullSuccess = true
	for _, status := range result.InstanceStatuses {
		if !status.Success {
			result.FullSuccess = false
			break
		}
	}

	if result.FullSuccess {
		os.Remove(backupPrivPath)
		os.Remove(backupPubPath)
		log.Printf("SSH key rotation: complete, all %d instances updated (new fingerprint: %s)", len(instances), result.NewFingerprint)
	} else {
		log.Printf("SSH key rotation: partial success, backup files retained at %s (new fingerprint: %s)", keyDir, result.NewFingerprint)
	}

	return result, nil
}

// appendPublicKey appends a public key to an instance's authorized_keys file
// using the orchestrator's exec mechanism (docker exec or kubectl exec).
func appendPublicKey(ctx context.Context, orch RotationOrchestrator, instanceName string, publicKey string) error {
	b64 := base64.StdEncoding.EncodeToString([]byte(publicKey))
	cmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d >> /root/.ssh/authorized_keys", b64)}
	_, stderr, code, err := orch.ExecInInstance(ctx, instanceName, cmd)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("exit code %d: %s", code, stderr)
	}
	return nil
}

// copyFile copies src to dst, preserving the source file's permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// restoreFile copies src back to dst and removes src on success.
// Errors are logged but not returned (best-effort recovery).
func restoreFile(src, dst string) {
	if err := copyFile(src, dst); err != nil {
		log.Printf("SSH key rotation: failed to restore %s from %s: %v", dst, src, err)
		return
	}
	os.Remove(src)
}
