//go:build docker_integration

package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/browserprov"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/handlers"
	"github.com/gluk-w/claworc/control-plane/internal/llmgateway"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

var (
	sessionURL         string // base URL shared by all tests
	sessionGatewayPort int
)

// launchEmbeddedServer spins up the full Claworc server in-process using httptest.NewServer.
// Returns the base URL, a context cancel function, and a cleanup function.
func launchEmbeddedServer() (string, context.CancelFunc, func()) {
	dataDir, err := os.MkdirTemp("", "claworc-inttest-*")
	if err != nil {
		log.Fatalf("create temp dir: %v", err)
	}

	os.Setenv("CLAWORC_AUTH_DISABLED", "true")
	os.Setenv("CLAWORC_DATA_PATH", dataDir)

	config.Load()

	if err := database.Init(); err != nil {
		log.Fatalf("database init: %v", err)
	}
	if err := database.InitLogsDB(dataDir); err != nil {
		log.Fatalf("logs database init: %v", err)
	}

	if err := database.SetSetting("orchestrator_backend", "docker"); err != nil {
		log.Fatalf("seed orchestrator_backend: %v", err)
	}

	if img := os.Getenv("AGENT_TEST_IMAGE"); img != "" {
		if err := database.SetSetting("default_container_image", img); err != nil {
			log.Printf("Warning: failed to set default_container_image: %v", err)
		}
	}
	if img := os.Getenv("BROWSER_TEST_IMAGE"); img != "" {
		if err := database.SetSetting("default_browser_image", img); err != nil {
			log.Printf("Warning: failed to set default_browser_image: %v", err)
		}
	}

	hash, err := auth.HashPassword("admin")
	if err != nil {
		log.Fatalf("hash password: %v", err)
	}
	if err := database.CreateUser(&database.User{
		Username:     "admin",
		PasswordHash: hash,
		Role:         "admin",
	}); err != nil {
		log.Fatalf("create admin user: %v", err)
	}

	// If CLAWORC_LLM_GATEWAY_PORT is explicitly set (e.g. 40001 from Makefile), use it as-is.
	// Otherwise pick a random free port so tests don't conflict with the local dev server.
	var gatewayPort int
	if os.Getenv("CLAWORC_LLM_GATEWAY_PORT") != "" {
		gatewayPort = config.Cfg.LLMGatewayPort
	} else {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Fatalf("find free gateway port: %v", err)
		}
		gatewayPort = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		config.Cfg.LLMGatewayPort = gatewayPort
	}

	sshSigner, sshPublicKey, err := sshproxy.EnsureKeyPair(dataDir)
	if err != nil {
		log.Fatalf("SSH key init: %v", err)
	}
	sshMgr := sshproxy.NewSSHManager(sshSigner, sshPublicKey)
	handlers.SSHMgr = sshMgr
	tunnelMgr := sshproxy.NewTunnelManager(sshMgr)
	handlers.TunnelMgr = tunnelMgr

	auditor, err := sshaudit.NewAuditor(database.DB, 90)
	if err != nil {
		log.Fatalf("audit init: %v", err)
	}
	handlers.AuditLog = auditor

	ctx, cancel := context.WithCancel(context.Background())

	auditor.StartRetentionCleanup(ctx)

	sshMgr.OnEvent(func(event sshproxy.ConnectionEvent) {
		switch event.Type {
		case sshproxy.EventConnected, sshproxy.EventReconnected:
			auditor.LogConnection(event.InstanceID, "system", event.Details)
		case sshproxy.EventDisconnected:
			auditor.LogDisconnection(event.InstanceID, "system", event.Details)
		case sshproxy.EventKeyUploaded:
			auditor.LogKeyUpload(event.InstanceID, event.Details)
		}
	})

	termMgr := sshterminal.NewSessionManager(sshterminal.SessionManagerConfig{
		HistoryLines: 100,
		IdleTimeout:  5 * time.Minute,
	})
	handlers.TermSessionMgr = termMgr

	sessionStore := auth.NewSessionStore()
	handlers.SessionStore = sessionStore

	if err := orchestrator.InitOrchestrator(ctx); err != nil {
		log.Fatalf("orchestrator init: %v", err)
	}

	if err := llmgateway.Start(ctx, "127.0.0.1", gatewayPort); err != nil {
		log.Fatalf("LLM gateway start: %v", err)
	}
	tunnelMgr.SetLLMGatewayAddr(fmt.Sprintf("127.0.0.1:%d", gatewayPort))

	if orch := orchestrator.Get(); orch != nil {
		sshMgr.SetOrchestrator(orch)
	}
	sshMgr.StartHealthChecker(ctx)

	// Wire TaskManager + on-demand BrowserBridge so /browser/* and tunnel
	// CDP routing behave like the production server.
	taskMgr := taskmanager.New(taskmanager.Config{})
	handlers.TaskMgr = taskMgr
	if orch := orchestrator.Get(); orch != nil {
		provider := browserprov.NewLocalProvider(orch, sshMgr)
		bridge := browserprov.New(provider, taskMgr)
		bridge.Start(ctx)
		handlers.BrowserBridgeRef = bridge
		handlers.BrowserStopper = browserprov.StopperAdapter{Provider: provider}
		handlers.BrowserMigrator = browserprov.NewMigrator(taskMgr, orch, bridge)
		handlers.BrowserAdmin = browserprov.AdminAdapter{Provider: provider}

		tunnelMgr.SetCDPDialProvider(func(dctx context.Context, instanceID uint) (sshproxy.DialFunc, bool) {
			var inst database.Instance
			if err := database.DB.First(&inst, instanceID).Error; err != nil {
				return nil, false
			}
			if database.IsLegacyEmbedded(inst.ContainerImage) {
				return nil, false
			}
			return func(callCtx context.Context) (io.ReadWriteCloser, error) {
				return bridge.DialCDP(callCtx, instanceID)
			}, true
		})
	}

	orchestrator.SetInstanceFactory(func(fctx context.Context, name string) (sshproxy.Instance, error) {
		var inst database.Instance
		if err := database.DB.Where("name = ?", name).First(&inst).Error; err != nil {
			return nil, fmt.Errorf("instance not found: %s", name)
		}
		client, err := sshMgr.WaitForSSH(fctx, inst.ID, 120*time.Second)
		if err != nil {
			return nil, err
		}
		return sshproxy.NewSSHInstance(client), nil
	})

	if orch := orchestrator.Get(); orch != nil {
		tunnelMgr.StartBackgroundManager(ctx, func(bctx context.Context) ([]uint, error) {
			var instances []database.Instance
			if err := database.DB.Where("status = ?", "running").Find(&instances).Error; err != nil {
				return nil, err
			}
			ids := make([]uint, len(instances))
			for i, inst := range instances {
				ids[i] = inst.ID
			}
			return ids, nil
		}, orch)
		tunnelMgr.StartTunnelHealthChecker(ctx)
	}

	handlers.StartKeyRotationJob(ctx)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Get("/health", handlers.HealthCheck)
	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(sessionStore))

			r.Get("/instances/{id}", handlers.GetInstance)
			r.Get("/instances/{id}/ssh-status", handlers.GetSSHStatus)

			// Tasks API — clone tests poll task state until terminal.
			r.Get("/tasks", handlers.ListTasks)
			r.Get("/tasks/{id}", handlers.GetTask)
			r.Post("/tasks/{id}/cancel", handlers.CancelTask)

			// On-demand browser pod controls.
			r.Get("/instances/{id}/browser/status", handlers.BrowserStatus)
			r.Post("/instances/{id}/browser/start", handlers.BrowserStart)
			r.Post("/instances/{id}/browser/stop", handlers.BrowserStop)

			r.Get("/instances/{id}/terminal", handlers.TerminalWSProxy)
			r.Get("/instances/{id}/terminal/sessions", handlers.ListTerminalSessions)
			r.Delete("/instances/{id}/terminal/sessions/{sessionId}", handlers.CloseTerminalSession)
			r.Get("/instances/{id}/logs", handlers.StreamLogs)

			// Files
			r.Get("/instances/{id}/files/browse", handlers.BrowseFiles)
			r.Get("/instances/{id}/files/read", handlers.ReadFileContent)
			r.Get("/instances/{id}/files/download", handlers.DownloadFile)
			r.Post("/instances/{id}/files/create", handlers.CreateNewFile)
			r.Post("/instances/{id}/files/mkdir", handlers.CreateDirectory)
			r.Post("/instances/{id}/files/upload", handlers.UploadFile)
			r.Delete("/instances/{id}/files", handlers.DeleteFile)
			r.Post("/instances/{id}/files/rename", handlers.RenameFile)
			r.Get("/instances/{id}/files/search", handlers.SearchFiles)

			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdmin)

				r.Post("/instances", handlers.CreateInstance)
				r.Post("/instances/{id}/clone", handlers.CloneInstance)
				r.Post("/instances/{id}/restart", handlers.RestartInstance)
				r.Delete("/instances/{id}", handlers.DeleteInstance)

				// Skills
				r.Get("/skills", handlers.ListSkills)
				r.Post("/skills", handlers.UploadSkill)
				r.Delete("/skills/{slug}", handlers.DeleteSkill)
				r.Get("/skills/{slug}/files", handlers.ListSkillFiles)
				r.Get("/skills/{slug}/files/*", handlers.GetSkillFile)
				r.Put("/skills/{slug}/files/*", handlers.PutSkillFile)

				r.Post("/llm/providers", handlers.CreateProvider)
				r.Delete("/llm/providers/{id}", handlers.DeleteProvider)

				// Backups
				r.Post("/instances/{id}/backups", handlers.CreateBackup)
				r.Get("/instances/{id}/backups", handlers.ListInstanceBackups)
				r.Get("/backups", handlers.ListAllBackups)
				r.Get("/backups/{backupId}", handlers.GetBackupDetail)
				r.Delete("/backups/{backupId}", handlers.DeleteBackupHandler)
				r.Post("/backups/{backupId}/restore", handlers.RestoreBackupHandler)
				r.Get("/backups/{backupId}/download", handlers.DownloadBackup)

				// Backup Schedules
				r.Post("/backup-schedules", handlers.CreateBackupSchedule)
				r.Get("/backup-schedules", handlers.ListBackupSchedules)
				r.Put("/backup-schedules/{id}", handlers.UpdateBackupSchedule)
				r.Delete("/backup-schedules/{id}", handlers.DeleteBackupSchedule)

				// Shared Folders
				r.Get("/shared-folders", handlers.ListSharedFolders)
				r.Post("/shared-folders", handlers.CreateSharedFolder)
				r.Get("/shared-folders/{id}", handlers.GetSharedFolder)
				r.Put("/shared-folders/{id}", handlers.UpdateSharedFolder)
				r.Delete("/shared-folders/{id}", handlers.DeleteSharedFolder)
			})
		})
	})

	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(sessionStore))
		r.HandleFunc("/openclaw/{id}/*", handlers.ControlProxy)
	})

	ts := httptest.NewServer(r)

	cleanup := func() {
		ts.Close()
		termMgr.Stop()
		taskMgr.Close()
		database.Close()
		os.RemoveAll(dataDir)
	}

	return ts.URL, cancel, cleanup
}

func TestMain(m *testing.M) {
	url, cancel, cleanup := launchEmbeddedServer()
	sessionURL = url
	sessionGatewayPort = config.Cfg.LLMGatewayPort
	code := m.Run()
	cancel()
	cleanup()
	os.Exit(code)
}

func TestIntegration_InstanceLifecycle_ConfiguresOpenclaw(t *testing.T) {
	baseURL := sessionURL

	client := &http.Client{Timeout: 60 * time.Second}

	// --- Step 1: Create LLM provider ---
	provBody, _ := json.Marshal(map[string]interface{}{
		"key":      "test-openai",
		"name":     "Test OpenAI",
		"base_url": "https://api.openai.com/v1",
		"api_type": "openai-completions",
	})
	resp, err := client.Post(baseURL+"/api/v1/llm/providers", "application/json", bytes.NewReader(provBody))
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create provider: expected 201, got %d (body: %s)", resp.StatusCode, string(body))
	}
	var provResp struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&provResp)
	resp.Body.Close()
	provID := provResp.ID
	t.Logf("Created provider id=%d", provID)

	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/llm/providers/%d", baseURL, provID), nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Warning: delete provider id=%d: %v", provID, err)
			return
		}
		resp.Body.Close()
		t.Logf("Deleted provider id=%d", provID)
	}()

	// --- Step 2: Create instance ---
	displayName := fmt.Sprintf("inttest-%d", time.Now().UnixNano())
	instBody, _ := json.Marshal(map[string]interface{}{
		"display_name":      displayName,
		"models":            map[string]interface{}{"extra": []string{"test-model"}},
		"enabled_providers": []uint{provID},
	})
	resp, err = client.Post(baseURL+"/api/v1/instances", "application/json", bytes.NewReader(instBody))
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
			t.Logf("Warning: delete instance id=%d: %v", instID, err)
			return
		}
		resp.Body.Close()
		t.Logf("Deleted instance id=%d name=%s", instID, instName)

		// Verify the container is gone
		out, err := exec.Command("docker", "inspect", instName).CombinedOutput()
		if err == nil {
			t.Errorf("container %s still exists after delete: %s", instName, string(out))
		} else {
			t.Logf("Container %s removed (docker inspect failed as expected)", instName)
		}
	}()

	// --- Step 3: Poll until instance status == "running" ---
	t.Log("Polling instance status until running...")
	deadline := time.Now().Add(120 * time.Second)
	var running bool
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", baseURL, instID))
		if err != nil {
			t.Logf("get instance: %v — retrying", err)
			time.Sleep(2 * time.Second)
			continue
		}
		var pollResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&pollResp)
		resp.Body.Close()

		status, _ := pollResp["status"].(string)
		t.Logf("Instance status: %s (message: %v)", status, pollResp["status_message"])
		if status == "running" {
			running = true
			break
		}
		if status == "error" {
			t.Fatalf("Instance entered error status: %v", pollResp["status_message"])
		}
		time.Sleep(2 * time.Second)
	}
	if !running {
		t.Fatal("Instance did not reach 'running' status within 120s")
	}

	// --- Step 4: Poll docker exec to verify openclaw.json has models.providers set ---
	t.Log("Polling openclaw.json for models.providers configuration...")

	type providerEntry struct {
		BaseURL string `json:"baseUrl"`
		API     string `json:"api"`
		APIKey  string `json:"apiKey"`
	}
	type openClawConfig struct {
		Models struct {
			Providers map[string]providerEntry `json:"providers"`
		} `json:"models"`
		Agents struct {
			Defaults struct {
				Model struct {
					Primary   string   `json:"primary"`
					Fallbacks []string `json:"fallbacks"`
				} `json:"model"`
			} `json:"defaults"`
		} `json:"agents"`
	}

	var finalCfg openClawConfig
	deadline = time.Now().Add(90 * time.Second)
	configured := false
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", instName, "cat", orchestrator.PathOpenClawConfig).Output()
		if err != nil {
			t.Logf("docker exec cat openclaw.json: %v — retrying", err)
			time.Sleep(3 * time.Second)
			continue
		}

		var cfg openClawConfig
		if err := json.Unmarshal(out, &cfg); err != nil {
			t.Logf("parse openclaw.json: %v (raw: %s) — retrying", err, strings.TrimSpace(string(out)))
			time.Sleep(3 * time.Second)
			continue
		}

		if len(cfg.Models.Providers) > 0 {
			t.Logf("models.providers configured with %d provider(s)", len(cfg.Models.Providers))
			finalCfg = cfg
			configured = true
			break
		}
		t.Log("models.providers not yet set — retrying")
		time.Sleep(3 * time.Second)
	}
	if !configured {
		t.Fatal("models.providers was not configured within 90s — ConfigureInstance may not have run")
	}

	// --- Step 5: Assertions ---

	// Assert models.providers["test-openai"] exists with correct baseUrl
	prov, ok := finalCfg.Models.Providers["test-openai"]
	if !ok {
		t.Errorf("models.providers[\"test-openai\"] not found; got keys: %v", providerKeys(finalCfg.Models.Providers))
	} else {
		expectedBaseURL := fmt.Sprintf("http://127.0.0.1:%d", sessionGatewayPort)
		if prov.BaseURL != expectedBaseURL {
			t.Errorf("models.providers[test-openai].baseUrl = %q, want %q", prov.BaseURL, expectedBaseURL)
		} else {
			t.Logf("models.providers[test-openai].baseUrl = %q ✓", prov.BaseURL)
		}
	}

	// Assert agents.defaults.model.primary == "test-model"
	primary := finalCfg.Agents.Defaults.Model.Primary
	if primary != "test-model" {
		t.Errorf("agents.defaults.model.primary = %q, want %q", primary, "test-model")
	} else {
		t.Logf("agents.defaults.model.primary = %q ✓", primary)
	}
}

// providerKeys returns the keys of a map for logging.
func providerKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestIntegration_SharedFolder_MountedAndWritable(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 60 * time.Second}

	// --- Step 1: Create an instance ---
	displayName := fmt.Sprintf("sf-test-%d", time.Now().UnixNano())
	instBody, _ := json.Marshal(map[string]interface{}{
		"display_name": displayName,
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
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		t.Logf("Deleted instance id=%d", instID)
	}()

	// Wait for instance to be running
	t.Log("Waiting for instance to be running...")
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", baseURL, instID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var poll map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&poll)
		resp.Body.Close()
		status, _ := poll["status"].(string)
		if status == "running" {
			break
		}
		if status == "error" {
			t.Fatalf("Instance entered error status: %v", poll["status_message"])
		}
		if time.Now().After(deadline) {
			t.Fatal("Instance did not reach 'running' within 120s")
		}
		time.Sleep(2 * time.Second)
	}

	// --- Step 2: Create a shared folder ---
	sfBody, _ := json.Marshal(map[string]string{
		"name":       "test-shared",
		"mount_path": "/shared/test-data",
	})
	resp, err = client.Post(baseURL+"/api/v1/shared-folders", "application/json", bytes.NewReader(sfBody))
	if err != nil {
		t.Fatalf("create shared folder: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create shared folder: expected 201, got %d (body: %s)", resp.StatusCode, string(body))
	}
	var sfResp struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&sfResp)
	resp.Body.Close()
	sfID := sfResp.ID
	t.Logf("Created shared folder id=%d", sfID)

	// --- Step 3: Map the shared folder to the instance (triggers auto-restart) ---
	updateBody, _ := json.Marshal(map[string]interface{}{
		"instance_ids": []uint{instID},
	})
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/shared-folders/%d", baseURL, sfID), bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update shared folder: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("update shared folder: expected 200, got %d (body: %s)", resp.StatusCode, string(body))
	}
	resp.Body.Close()
	t.Log("Mapped shared folder to instance (auto-restart triggered)")

	// --- Step 4: Wait for instance to be running again after restart ---
	t.Log("Waiting for instance to be running after restart...")
	time.Sleep(5 * time.Second) // Give restart time to initiate
	deadline = time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", baseURL, instID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var poll map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&poll)
		resp.Body.Close()
		status, _ := poll["status"].(string)
		if status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Instance did not reach 'running' after restart within 120s")
		}
		time.Sleep(2 * time.Second)
	}

	// --- Step 5: Verify mount exists and is writable by claworc user ---
	t.Log("Verifying shared folder is mounted and writable...")
	deadline = time.Now().Add(30 * time.Second)
	verified := false
	for time.Now().Before(deadline) {
		// Check mount exists
		out, err := exec.Command("docker", "exec", instName, "stat", "/shared/test-data").CombinedOutput()
		if err != nil {
			t.Logf("stat /shared/test-data: %v — retrying", err)
			time.Sleep(2 * time.Second)
			continue
		}
		t.Logf("Mount exists: %s", strings.TrimSpace(string(out)))

		// Write a file as claworc user
		out, err = exec.Command("docker", "exec", "--user", "claworc", instName,
			"sh", "-c", "echo hello > /shared/test-data/testfile.txt && cat /shared/test-data/testfile.txt").CombinedOutput()
		if err != nil {
			t.Logf("write test: %v (output: %s) — retrying", err, strings.TrimSpace(string(out)))
			time.Sleep(2 * time.Second)
			continue
		}
		content := strings.TrimSpace(string(out))
		if content != "hello" {
			t.Fatalf("unexpected file content: %q, want %q", content, "hello")
		}
		t.Log("Shared folder is mounted and writable by claworc user ✓")
		verified = true
		break
	}
	if !verified {
		t.Fatal("Failed to verify shared folder mount within 30s")
	}

	// --- Step 6: Delete the shared folder and verify volume is cleaned up ---
	volName := fmt.Sprintf("claworc-shared-%d", sfID)

	// Verify volume exists before delete
	if _, err := exec.Command("docker", "volume", "inspect", volName).CombinedOutput(); err != nil {
		t.Fatalf("volume %s should exist before delete but doesn't: %v", volName, err)
	}
	t.Logf("Volume %s exists before delete ✓", volName)

	req, _ = http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/shared-folders/%d", baseURL, sfID), nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("delete shared folder: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("delete shared folder: expected 204, got %d (body: %s)", resp.StatusCode, string(body))
	}
	resp.Body.Close()
	t.Log("Deleted shared folder via API")

	// Wait for background volume deletion (handler sleeps 10s before deleting)
	t.Log("Waiting for background volume deletion...")
	deadline = time.Now().Add(30 * time.Second)
	volumeDeleted := false
	for time.Now().Before(deadline) {
		if _, err := exec.Command("docker", "volume", "inspect", volName).CombinedOutput(); err != nil {
			t.Logf("Volume %s deleted ✓", volName)
			volumeDeleted = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !volumeDeleted {
		t.Fatalf("Volume %s was not deleted within 30s after shared folder deletion", volName)
	}
}

// TestIntegration_SharedFolder_RemoveInstanceUnmounts verifies that when an
// instance is removed from a shared folder's instance list, the instance
// auto-restarts and the mount disappears from inside the container.
func TestIntegration_SharedFolder_RemoveInstanceUnmounts(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 60 * time.Second}

	// --- Step 1: Create instance ---
	displayName := fmt.Sprintf("sf-unmount-%d", time.Now().UnixNano())
	instBody, _ := json.Marshal(map[string]interface{}{"display_name": displayName})
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
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		t.Logf("Deleted instance id=%d", instID)
	}()

	waitRunning := func(label string) {
		t.Helper()
		t.Logf("Waiting for instance to be running (%s)...", label)
		deadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", baseURL, instID))
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			var poll map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&poll)
			resp.Body.Close()
			status, _ := poll["status"].(string)
			if status == "running" {
				return
			}
			if status == "error" {
				t.Fatalf("Instance entered error status (%s): %v", label, poll["status_message"])
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("Instance did not reach 'running' (%s) within 120s", label)
	}

	waitRunning("initial")

	// --- Step 2: Create shared folder ---
	mountPath := "/shared/unmount-test"
	sfBody, _ := json.Marshal(map[string]string{
		"name":       "unmount-test",
		"mount_path": mountPath,
	})
	resp, err = client.Post(baseURL+"/api/v1/shared-folders", "application/json", bytes.NewReader(sfBody))
	if err != nil {
		t.Fatalf("create shared folder: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create shared folder: expected 201, got %d (body: %s)", resp.StatusCode, string(body))
	}
	var sfResp struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&sfResp)
	resp.Body.Close()
	sfID := sfResp.ID
	t.Logf("Created shared folder id=%d", sfID)

	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/shared-folders/%d", baseURL, sfID), nil)
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}()

	updateMembership := func(ids []uint) {
		t.Helper()
		body, _ := json.Marshal(map[string]interface{}{"instance_ids": ids})
		req, _ := http.NewRequest(http.MethodPut,
			fmt.Sprintf("%s/api/v1/shared-folders/%d", baseURL, sfID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("update shared folder: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("update shared folder %v: expected 200, got %d (body: %s)", ids, resp.StatusCode, string(b))
		}
	}

	// --- Step 3: Map folder to instance and wait for restart to settle ---
	updateMembership([]uint{instID})
	t.Log("Mapped shared folder to instance (auto-restart triggered)")
	time.Sleep(5 * time.Second)
	waitRunning("after-mount")

	// --- Step 4: Verify mount exists ---
	verifyMountPresent := func() {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			out, err := exec.Command("docker", "exec", instName, "stat", mountPath).CombinedOutput()
			if err == nil {
				t.Logf("Mount present: %s", strings.TrimSpace(string(out)))
				return
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("Mount %s did not appear within 30s", mountPath)
	}
	verifyMountPresent()

	// --- Step 5: Remove instance from folder; wait for second restart ---
	updateMembership([]uint{})
	t.Log("Removed instance from shared folder (auto-restart triggered)")
	time.Sleep(5 * time.Second)
	waitRunning("after-unmount")

	// --- Step 6: Verify mount is gone ---
	deadline := time.Now().Add(30 * time.Second)
	gone := false
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", instName, "stat", mountPath).CombinedOutput()
		if err != nil {
			t.Logf("Mount gone ✓ (stat err: %v, output: %s)", err, strings.TrimSpace(string(out)))
			gone = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !gone {
		t.Fatalf("Mount %s still present after instance was removed from shared folder", mountPath)
	}
}

// --- shared helpers for the new shared-folder integration tests ---

// sfCreateInstance creates an instance via the API and returns (id, name).
// The caller is responsible for deferring deletion.
func sfCreateInstance(t *testing.T, baseURL, label string) (uint, string) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	displayName := fmt.Sprintf("%s-%d", label, time.Now().UnixNano())
	body, _ := json.Marshal(map[string]interface{}{"display_name": displayName})
	resp, err := client.Post(baseURL+"/api/v1/instances", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create instance: expected 201, got %d (body: %s)", resp.StatusCode, string(b))
	}
	var out struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.ID, out.Name
}

// sfDeleteInstance issues DELETE /api/v1/instances/{id}.
func sfDeleteInstance(t *testing.T, baseURL string, id uint) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/instances/%d", baseURL, id), nil)
	if resp, _ := client.Do(req); resp != nil {
		resp.Body.Close()
	}
}

// sfWaitRunning polls instance status until "running" or fails the test.
func sfWaitRunning(t *testing.T, baseURL string, id uint, label string) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	t.Logf("Waiting for instance %d to be running (%s)...", id, label)
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", baseURL, id))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var poll map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&poll)
		resp.Body.Close()
		status, _ := poll["status"].(string)
		if status == "running" {
			return
		}
		if status == "error" {
			t.Fatalf("Instance %d entered error status (%s): %v", id, label, poll["status_message"])
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("Instance %d did not reach 'running' (%s) within 120s", id, label)
}

// sfCreateFolder creates a shared folder via the API and returns its ID.
func sfCreateFolder(t *testing.T, baseURL, name, mountPath string) uint {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	body, _ := json.Marshal(map[string]string{"name": name, "mount_path": mountPath})
	resp, err := client.Post(baseURL+"/api/v1/shared-folders", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create shared folder: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create shared folder: expected 201, got %d (body: %s)", resp.StatusCode, string(b))
	}
	var out struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.ID
}

// sfDeleteFolder issues DELETE /api/v1/shared-folders/{id}.
func sfDeleteFolder(t *testing.T, baseURL string, id uint) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/shared-folders/%d", baseURL, id), nil)
	if resp, _ := client.Do(req); resp != nil {
		resp.Body.Close()
	}
}

// sfMapFolderToInstances PUTs the given instance_ids on a shared folder.
func sfMapFolderToInstances(t *testing.T, baseURL string, folderID uint, instIDs []uint) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	body, _ := json.Marshal(map[string]interface{}{"instance_ids": instIDs})
	req, _ := http.NewRequest(http.MethodPut,
		fmt.Sprintf("%s/api/v1/shared-folders/%d", baseURL, folderID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("update shared folder: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("update shared folder: expected 200, got %d (body: %s)", resp.StatusCode, string(b))
	}
}

// TestIntegration_SharedFolder_MultiInstanceShared verifies that a single
// shared folder mapped to two instances actually shares one volume — a file
// written from instance A is readable from instance B.
func TestIntegration_SharedFolder_MultiInstanceShared(t *testing.T) {
	baseURL := sessionURL

	idA, nameA := sfCreateInstance(t, baseURL, "sf-multi-a")
	defer sfDeleteInstance(t, baseURL, idA)
	idB, nameB := sfCreateInstance(t, baseURL, "sf-multi-b")
	defer sfDeleteInstance(t, baseURL, idB)

	sfWaitRunning(t, baseURL, idA, "A initial")
	sfWaitRunning(t, baseURL, idB, "B initial")

	mountPath := "/shared/multi-test"
	sfID := sfCreateFolder(t, baseURL, "multi-test", mountPath)
	defer sfDeleteFolder(t, baseURL, sfID)

	sfMapFolderToInstances(t, baseURL, sfID, []uint{idA, idB})
	time.Sleep(5 * time.Second)
	sfWaitRunning(t, baseURL, idA, "A after-mount")
	sfWaitRunning(t, baseURL, idB, "B after-mount")

	// Wait for the mount to appear inside both containers.
	waitMount := func(name string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := exec.Command("docker", "exec", name, "stat", mountPath).CombinedOutput(); err == nil {
				return
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("Mount %s did not appear in %s within 30s", mountPath, name)
	}
	waitMount(nameA)
	waitMount(nameB)

	// Write from A.
	out, err := exec.Command("docker", "exec", "--user", "claworc", nameA,
		"sh", "-c", fmt.Sprintf("echo from-A > %s/handoff.txt", mountPath)).CombinedOutput()
	if err != nil {
		t.Fatalf("write from A: %v (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// Read from B, retrying briefly to absorb any FS sync lag.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", "--user", "claworc", nameB,
			"sh", "-c", fmt.Sprintf("cat %s/handoff.txt", mountPath)).CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "from-A" {
			t.Log("Volume is genuinely shared between A and B ✓")
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("instance B never saw the file written by A at %s/handoff.txt", mountPath)
}

// TestIntegration_SharedFolder_PersistenceAcrossRestart verifies that data
// written into a shared folder survives an explicit restart of the instance.
func TestIntegration_SharedFolder_PersistenceAcrossRestart(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 60 * time.Second}

	instID, instName := sfCreateInstance(t, baseURL, "sf-persist")
	defer sfDeleteInstance(t, baseURL, instID)
	sfWaitRunning(t, baseURL, instID, "initial")

	mountPath := "/shared/persist-test"
	sfID := sfCreateFolder(t, baseURL, "persist-test", mountPath)
	defer sfDeleteFolder(t, baseURL, sfID)

	sfMapFolderToInstances(t, baseURL, sfID, []uint{instID})
	time.Sleep(5 * time.Second)
	sfWaitRunning(t, baseURL, instID, "after-mount")

	// Write a marker file.
	out, err := exec.Command("docker", "exec", "--user", "claworc", instName,
		"sh", "-c", fmt.Sprintf("echo persistent > %s/marker.txt", mountPath)).CombinedOutput()
	if err != nil {
		t.Fatalf("write marker: %v (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// Trigger an explicit instance restart via the API.
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/v1/instances/%d/restart", baseURL, instID), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("restart instance: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("restart instance: expected 2xx, got %d", resp.StatusCode)
	}
	time.Sleep(5 * time.Second)
	sfWaitRunning(t, baseURL, instID, "after-restart")

	// Marker must still be there.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", "--user", "claworc", instName,
			"sh", "-c", fmt.Sprintf("cat %s/marker.txt", mountPath)).CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "persistent" {
			t.Log("Marker survived restart ✓")
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("marker file disappeared after restart")
}

// TestIntegration_SharedFolder_DeleteWithRunningInstances verifies that
// deleting a shared folder while two instances have it mounted causes both
// to restart, drops the mount from both, and cleans up the volume.
func TestIntegration_SharedFolder_DeleteWithRunningInstances(t *testing.T) {
	baseURL := sessionURL

	idA, nameA := sfCreateInstance(t, baseURL, "sf-delmulti-a")
	defer sfDeleteInstance(t, baseURL, idA)
	idB, nameB := sfCreateInstance(t, baseURL, "sf-delmulti-b")
	defer sfDeleteInstance(t, baseURL, idB)
	sfWaitRunning(t, baseURL, idA, "A initial")
	sfWaitRunning(t, baseURL, idB, "B initial")

	mountPath := "/shared/delmulti-test"
	sfID := sfCreateFolder(t, baseURL, "delmulti-test", mountPath)
	sfMapFolderToInstances(t, baseURL, sfID, []uint{idA, idB})
	time.Sleep(5 * time.Second)
	sfWaitRunning(t, baseURL, idA, "A after-mount")
	sfWaitRunning(t, baseURL, idB, "B after-mount")

	volName := fmt.Sprintf("claworc-shared-%d", sfID)
	if _, err := exec.Command("docker", "volume", "inspect", volName).CombinedOutput(); err != nil {
		t.Fatalf("volume %s should exist before delete: %v", volName, err)
	}

	// Delete the shared folder; both instances should restart and the volume be cleaned up.
	sfDeleteFolder(t, baseURL, sfID)
	time.Sleep(5 * time.Second)
	sfWaitRunning(t, baseURL, idA, "A after-delete")
	sfWaitRunning(t, baseURL, idB, "B after-delete")

	// Mount must be gone in both.
	for _, name := range []string{nameA, nameB} {
		deadline := time.Now().Add(30 * time.Second)
		gone := false
		for time.Now().Before(deadline) {
			if _, err := exec.Command("docker", "exec", name, "stat", mountPath).CombinedOutput(); err != nil {
				gone = true
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !gone {
			t.Fatalf("Mount %s still present in %s after folder delete", mountPath, name)
		}
	}

	// Volume must eventually be deleted (handler sleeps 10s before).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := exec.Command("docker", "volume", "inspect", volName).CombinedOutput(); err != nil {
			t.Logf("Volume %s deleted ✓", volName)
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("Volume %s not deleted within 30s after folder delete", volName)
}
