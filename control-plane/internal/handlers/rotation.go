package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
	"gorm.io/gorm"
)

// RotateSSHKey handles POST /api/v1/settings/rotate-ssh-key.
// It triggers a global SSH key rotation across all running instances.
func RotateSSHKey(w http.ResponseWriter, r *http.Request) {
	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeError(w, http.StatusServiceUnavailable, "Orchestrator not available")
		return
	}

	// Fetch running instances from DB
	var dbInstances []database.Instance
	if err := database.DB.Where("status = ?", "running").Find(&dbInstances).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to query instances")
		return
	}

	instances := make([]sshkeys.InstanceInfo, len(dbInstances))
	for i, inst := range dbInstances {
		instances[i] = sshkeys.InstanceInfo{ID: inst.ID, Name: inst.Name}
	}

	result, err := sshkeys.RotateGlobalKeyPair(r.Context(), config.Cfg.DataPath, instances, orch, SSHMgr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Key rotation failed: "+err.Error())
		return
	}

	// Record rotation timestamp
	database.SetSetting("ssh_key_last_rotation", result.Timestamp.UTC().Format(time.RFC3339))

	// Audit log the key rotation
	successCount := 0
	for _, s := range result.InstanceStatuses {
		if s.Success {
			successCount++
		}
	}
	auditLog(sshaudit.EventKeyRotation, 0, getUsername(r),
		fmt.Sprintf("old=%s, new=%s, instances=%d/%d succeeded",
			result.OldFingerprint, result.NewFingerprint,
			successCount, len(result.InstanceStatuses)))

	writeJSON(w, http.StatusOK, result)
}

// StartKeyRotationJob starts a background goroutine that checks daily whether
// the SSH key needs to be rotated based on ssh_key_rotation_policy_days.
// It returns a cancel function to stop the background job.
func StartKeyRotationJob(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				checkAndRotateKeys(ctx)
			}
		}
	}()

	return cancel
}

func checkAndRotateKeys(ctx context.Context) {
	if SSHMgr == nil {
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		log.Printf("SSH key rotation job: orchestrator not available, skipping")
		return
	}

	// Read policy days
	policyStr, err := database.GetSetting("ssh_key_rotation_policy_days")
	if err != nil {
		log.Printf("SSH key rotation job: failed to read policy: %v", err)
		return
	}
	policyDays, err := strconv.Atoi(policyStr)
	if err != nil || policyDays <= 0 {
		log.Printf("SSH key rotation job: invalid policy_days %q, skipping", policyStr)
		return
	}

	// Read last rotation timestamp
	lastRotationStr, err := database.GetSetting("ssh_key_last_rotation")
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Printf("SSH key rotation job: failed to read last rotation: %v", err)
		return
	}

	var lastRotation time.Time
	if lastRotationStr != "" {
		lastRotation, err = time.Parse(time.RFC3339, lastRotationStr)
		if err != nil {
			log.Printf("SSH key rotation job: invalid last_rotation %q, treating as never rotated", lastRotationStr)
		}
	}
	// If lastRotation is zero (never rotated), it will be older than any policy threshold

	if time.Since(lastRotation) < time.Duration(policyDays)*24*time.Hour {
		return // Not due yet
	}

	log.Printf("SSH key rotation job: key is older than %d days, starting automatic rotation", policyDays)

	// Fetch running instances
	var dbInstances []database.Instance
	if err := database.DB.Where("status = ?", "running").Find(&dbInstances).Error; err != nil {
		log.Printf("SSH key rotation job: failed to query instances: %v", err)
		return
	}

	instances := make([]sshkeys.InstanceInfo, len(dbInstances))
	for i, inst := range dbInstances {
		instances[i] = sshkeys.InstanceInfo{ID: inst.ID, Name: inst.Name}
	}

	result, err := sshkeys.RotateGlobalKeyPair(ctx, config.Cfg.DataPath, instances, orch, SSHMgr)
	if err != nil {
		log.Printf("SSH key rotation job: rotation failed: %v", err)
		return
	}

	// Record rotation timestamp
	database.SetSetting("ssh_key_last_rotation", result.Timestamp.UTC().Format(time.RFC3339))

	if result.FullSuccess {
		log.Printf("SSH key rotation job: automatic rotation complete (new fingerprint: %s)", result.NewFingerprint)
	} else {
		log.Printf("SSH key rotation job: automatic rotation partial success (new fingerprint: %s)", result.NewFingerprint)
	}

	// Audit log the automatic rotation
	successCount := 0
	for _, s := range result.InstanceStatuses {
		if s.Success {
			successCount++
		}
	}
	auditLog(sshaudit.EventKeyRotation, 0, "system",
		fmt.Sprintf("auto-rotation: old=%s, new=%s, instances=%d/%d succeeded",
			result.OldFingerprint, result.NewFingerprint,
			successCount, len(result.InstanceStatuses)))
}
