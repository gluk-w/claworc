//go:build docker_integration

package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestIntegration_BackupLifecycle exercises the full backup flow:
// create instance → create backup → poll until completed → list → download → delete.
func TestIntegration_BackupLifecycle(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 120 * time.Second}

	// --- Step 1: Create instance ---
	displayName := fmt.Sprintf("backup-test-%d", time.Now().UnixNano())
	instBody, _ := json.Marshal(map[string]interface{}{
		"display_name": displayName,
		"team_id":      1,
	})
	resp, err := client.Post(baseURL+"/api/v1/instances", "application/json", bytes.NewReader(instBody))
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create instance: expected 201, got %d (body: %s)", resp.StatusCode, string(body))
	}
	var instResp struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&instResp)
	resp.Body.Close()
	instID := instResp.ID
	instName := instResp.Name
	t.Logf("Created instance id=%d name=%s", instID, instName)

	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/instances/%d", baseURL, instID), nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Warning: delete instance: %v", err)
			return
		}
		resp.Body.Close()
		t.Logf("Deleted instance id=%d", instID)
	}()

	// --- Step 2: Wait for instance to be running ---
	t.Log("Waiting for instance to reach running status...")
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", baseURL, instID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var pollResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&pollResp)
		resp.Body.Close()
		status, _ := pollResp["status"].(string)
		if status == "running" {
			break
		}
		if status == "error" {
			t.Fatalf("Instance entered error status: %v", pollResp["status_message"])
		}
		time.Sleep(2 * time.Second)
	}

	// --- Step 3: Create a backup with custom paths ---
	t.Log("Creating backup...")
	backupBody, _ := json.Marshal(map[string]interface{}{
		"paths": []string{"HOME"},
		"note":  "integration test backup",
	})
	resp, err = client.Post(
		fmt.Sprintf("%s/api/v1/instances/%d/backups", baseURL, instID),
		"application/json",
		bytes.NewReader(backupBody),
	)
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create backup: expected 202, got %d (body: %s)", resp.StatusCode, string(body))
	}
	var backupResp struct {
		ID      uint   `json:"id"`
		Message string `json:"message"`
	}
	json.NewDecoder(resp.Body).Decode(&backupResp)
	resp.Body.Close()
	backupID := backupResp.ID
	t.Logf("Backup started: id=%d", backupID)

	// --- Step 4: Poll until backup completes ---
	t.Log("Waiting for backup to complete...")
	deadline = time.Now().Add(120 * time.Second)
	var backupCompleted bool
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/backups/%d", baseURL, backupID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var detail map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&detail)
		resp.Body.Close()
		status, _ := detail["status"].(string)
		t.Logf("Backup status: %s", status)
		if status == "completed" {
			backupCompleted = true
			sizeBytes, _ := detail["size_bytes"].(float64)
			t.Logf("Backup completed: size=%d bytes", int64(sizeBytes))
			if sizeBytes == 0 {
				t.Error("completed backup has zero size")
			}
			// Verify paths are stored
			paths, _ := detail["paths"].(string)
			if paths == "" {
				t.Error("expected paths to be stored in backup record")
			}
			break
		}
		if status == "failed" {
			errMsg, _ := detail["error_message"].(string)
			t.Fatalf("Backup failed: %s", errMsg)
		}
		time.Sleep(3 * time.Second)
	}
	if !backupCompleted {
		t.Fatal("Backup did not complete within 120s")
	}

	// --- Step 5: List instance backups ---
	t.Log("Listing instance backups...")
	resp, err = client.Get(fmt.Sprintf("%s/api/v1/instances/%d/backups", baseURL, instID))
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("list backups: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}
	var backupList []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&backupList)
	resp.Body.Close()
	if len(backupList) != 1 {
		t.Errorf("expected 1 backup, got %d", len(backupList))
	}

	// --- Step 6: List all backups ---
	resp, err = client.Get(baseURL + "/api/v1/backups")
	if err != nil {
		t.Fatalf("list all backups: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("list all backups: expected 200, got %d", resp.StatusCode)
		_ = body
	}
	var allBackupsResp struct {
		Backups []map[string]interface{} `json:"backups"`
	}
	json.NewDecoder(resp.Body).Decode(&allBackupsResp)
	resp.Body.Close()
	found := false
	for _, b := range allBackupsResp.Backups {
		if uint(b["id"].(float64)) == backupID {
			found = true
			break
		}
	}
	if !found {
		t.Error("backup not found in all-backups listing")
	}

	// --- Step 7: Download backup ---
	t.Log("Downloading backup...")
	resp, err = client.Get(fmt.Sprintf("%s/api/v1/backups/%d/download", baseURL, backupID))
	if err != nil {
		t.Fatalf("download backup: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("download: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}
	downloadBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(downloadBody) == 0 {
		t.Error("downloaded file is empty")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/gzip" {
		t.Errorf("expected Content-Type application/gzip, got %s", contentType)
	}
	t.Logf("Downloaded %d bytes", len(downloadBody))

	// --- Step 8: Delete backup ---
	t.Log("Deleting backup...")
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/backups/%d", baseURL, backupID), nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete backup: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("delete: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// Verify deletion
	resp, err = client.Get(fmt.Sprintf("%s/api/v1/backups/%d", baseURL, backupID))
	if err != nil {
		t.Fatalf("get deleted backup: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for deleted backup, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestIntegration_BackupScheduleCRUD exercises the schedule endpoints.
func TestIntegration_BackupScheduleCRUD(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 30 * time.Second}

	// --- Create schedule ---
	body, _ := json.Marshal(map[string]interface{}{
		"instance_ids":    "ALL",
		"cron_expression": "0 2 * * *",
		"paths":           []string{"HOME"},
	})
	resp, err := client.Post(baseURL+"/api/v1/backup-schedules", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create schedule: expected 201, got %d (body: %s)", resp.StatusCode, string(respBody))
	}
	var schedResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&schedResp)
	resp.Body.Close()

	schedID := uint(schedResp["id"].(float64))
	t.Logf("Created schedule id=%d", schedID)

	// Verify NextRunAt was set
	nextRunAt, ok := schedResp["next_run_at"].(string)
	if !ok || nextRunAt == "" {
		t.Error("expected next_run_at to be set on creation")
	}

	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/backup-schedules/%d", baseURL, schedID), nil)
		client.Do(req)
	}()

	// --- List schedules ---
	resp, err = client.Get(baseURL + "/api/v1/backup-schedules")
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list schedules: expected 200, got %d", resp.StatusCode)
	}
	var schedList []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&schedList)
	resp.Body.Close()
	if len(schedList) == 0 {
		t.Error("expected at least 1 schedule")
	}

	// --- Update schedule (change cron → NextRunAt should change) ---
	updateBody, _ := json.Marshal(map[string]interface{}{
		"cron_expression": "30 3 * * 1",
	})
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/api/v1/backup-schedules/%d", baseURL, schedID),
		bytes.NewReader(updateBody),
	)
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update schedule: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("update schedule: expected 200, got %d (body: %s)", resp.StatusCode, string(respBody))
	}
	var updateResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&updateResp)
	resp.Body.Close()

	newNextRunAt, ok := updateResp["next_run_at"].(string)
	if !ok || newNextRunAt == "" {
		t.Error("expected next_run_at to be recalculated after cron update")
	}
	if newNextRunAt == nextRunAt {
		t.Error("next_run_at should have changed after cron expression update")
	}
	t.Logf("NextRunAt updated: %s → %s", nextRunAt, newNextRunAt)

	// Verify cron expression was updated
	cronExpr, _ := updateResp["cron_expression"].(string)
	if cronExpr != "30 3 * * 1" {
		t.Errorf("expected cron '30 3 * * 1', got %q", cronExpr)
	}

	// --- Delete schedule ---
	req, _ = http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/backup-schedules/%d", baseURL, schedID), nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete schedule: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete schedule: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify deletion
	resp, _ = client.Get(fmt.Sprintf("%s/api/v1/backup-schedules/%d", baseURL, schedID))
	// Note: there's no GetBackupSchedule endpoint, but the list should not contain it
	resp2, _ := client.Get(baseURL + "/api/v1/backup-schedules")
	var finalList []map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&finalList)
	resp2.Body.Close()
	for _, s := range finalList {
		if uint(s["id"].(float64)) == schedID {
			t.Error("deleted schedule still appears in list")
		}
	}
}

// TestIntegration_BackupValidation tests error cases for backup endpoints.
func TestIntegration_BackupValidation(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 10 * time.Second}

	// Create backup for non-existent instance
	body, _ := json.Marshal(map[string]interface{}{
		"paths": []string{"HOME"},
	})
	resp, err := client.Post(
		baseURL+"/api/v1/instances/99999/backups",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent instance, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Delete non-existent backup
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/backups/99999", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	// Should return error (conflict or not found)
	if resp.StatusCode == http.StatusOK {
		t.Error("expected error for deleting non-existent backup")
	}
	resp.Body.Close()

	// Download non-existent backup
	resp, err = client.Get(baseURL + "/api/v1/backups/99999/download")
	if err != nil {
		t.Fatalf("download request: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent backup download, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Create schedule with invalid cron
	body, _ = json.Marshal(map[string]interface{}{
		"instance_ids":    "ALL",
		"cron_expression": "invalid cron",
		"paths":           []string{"HOME"},
	})
	resp, err = client.Post(baseURL+"/api/v1/backup-schedules", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid cron, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Create schedule without instance_ids
	body, _ = json.Marshal(map[string]interface{}{
		"cron_expression": "0 2 * * *",
	})
	resp, err = client.Post(baseURL+"/api/v1/backup-schedules", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing instance_ids, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
