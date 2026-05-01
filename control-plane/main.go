package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/backup"
	"github.com/gluk-w/claworc/control-plane/internal/browserprov"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/handlers"
	"github.com/gluk-w/claworc/control-plane/internal/llmgateway"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/moderator"
	"github.com/gluk-w/claworc/control-plane/internal/modwiring"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

//go:embed frontend/dist
var frontendFS embed.FS

var BuildDate string

func main() {
	// Handle CLI commands before starting the server
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--create-admin":
			runCLICommand("create-admin")
			return
		case "--reset-password":
			runCLICommand("reset-password")
			return
		}
	}

	config.Load()

	if err := database.Init(); err != nil {
		log.Fatalf("Database init: %v", err)
	}
	defer database.Close()

	if err := database.InitLogsDB(config.Cfg.DataPath); err != nil {
		log.Fatalf("Logs DB init: %v", err)
	}

	// Bootstrap the analytics installation ID so the very first telemetry
	// event has a stable identity. Errors here are non-fatal — analytics is
	// best-effort and never blocks the control plane from coming up.
	if _, err := analytics.GetOrCreateInstallationID(); err != nil {
		log.Printf("analytics installation id: %v", err)
	}

	log.Printf("Config: AuthDisabled=%v, RPID=%s, RPOrigins=%v", config.Cfg.AuthDisabled, config.Cfg.RPID, config.Cfg.RPOrigins)

	// Init global SSH key pair
	sshSigner, sshPublicKey, err := sshproxy.EnsureKeyPair(config.Cfg.DataPath)
	if err != nil {
		log.Fatalf("SSH key init: %v", err)
	}
	sshMgr := sshproxy.NewSSHManager(sshSigner, sshPublicKey)
	handlers.SSHMgr = sshMgr
	tunnelMgr := sshproxy.NewTunnelManager(sshMgr)
	handlers.TunnelMgr = tunnelMgr
	log.Printf("SSH manager initialized (public key: %d bytes)", len(sshPublicKey))

	// Init SSH audit logger
	retentionDays := 90
	if retStr, err := database.GetSetting("ssh_audit_retention_days"); err == nil {
		if d, err := strconv.Atoi(retStr); err == nil && d > 0 {
			retentionDays = d
		}
	}
	auditor, err := sshaudit.NewAuditor(database.DB, retentionDays)
	if err != nil {
		log.Fatalf("SSH audit init: %v", err)
	}
	handlers.AuditLog = auditor
	ctx := context.Background()
	cancelAuditCleanup := auditor.StartRetentionCleanup(ctx)
	_ = cancelAuditCleanup

	// Register audit listener for SSH connection events
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
	log.Printf("SSH audit logger initialized (retention=%d days)", retentionDays)

	// Init terminal session manager
	sessionTimeout, err := time.ParseDuration(config.Cfg.TerminalSessionTimeout)
	if err != nil {
		sessionTimeout = 30 * time.Minute
	}
	termMgr := sshterminal.NewSessionManager(sshterminal.SessionManagerConfig{
		HistoryLines: config.Cfg.TerminalHistoryLines,
		RecordingDir: config.Cfg.TerminalRecordingDir,
		IdleTimeout:  sessionTimeout,
	})
	handlers.TermSessionMgr = termMgr
	log.Printf("Terminal session manager initialized (history=%d lines, recording=%q, idle_timeout=%s)",
		config.Cfg.TerminalHistoryLines, config.Cfg.TerminalRecordingDir, sessionTimeout)

	// Init WebAuthn
	if err := auth.InitWebAuthn(config.Cfg.RPID, config.Cfg.RPOrigins); err != nil {
		log.Printf("WARNING: WebAuthn init failed: %v", err)
	}

	// Init session store
	sessionStore := auth.NewSessionStore()
	handlers.SessionStore = sessionStore

	// Session cleanup goroutine
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			sessionStore.Cleanup()
		}
	}()

	handlers.BuildDate = BuildDate

	if err := orchestrator.InitOrchestrator(ctx); err != nil {
		log.Printf("WARNING: %v", err)
	}

	// Initialize the TaskManager. It owns every long-running goroutine
	// started by user actions (instance create/restart/clone/update-image,
	// backup create, skill deploy). See docs/task-manager.md.
	taskMgr := taskmanager.New(taskmanager.Config{})
	handlers.TaskMgr = taskMgr
	backup.TaskMgr = taskMgr
	reconcileStuckTasks()

	// Start LLM gateway (internal only, reachable via SSH agent-listener tunnel)
	if err := llmgateway.Start(ctx, "127.0.0.1", config.Cfg.LLMGatewayPort); err != nil {
		log.Printf("WARNING: LLM gateway failed to start: %v", err)
	}
	tunnelMgr.SetLLMGatewayAddr(fmt.Sprintf("127.0.0.1:%d", config.Cfg.LLMGatewayPort))

	// Configure SSH manager with orchestrator for automatic reconnection
	if orch := orchestrator.Get(); orch != nil {
		sshMgr.SetOrchestrator(orch)
	}
	sshMgr.StartHealthChecker(ctx)

	// Build InstanceFactory: resolves an active SSH connection by instance name.
	instanceFactory := func(fctx context.Context, name string) (sshproxy.Instance, error) {
		var inst database.Instance
		if err := database.DB.Where("name = ?", name).First(&inst).Error; err != nil {
			return nil, fmt.Errorf("instance not found: %s", name)
		}
		client, err := sshMgr.WaitForSSH(fctx, inst.ID, 120*time.Second)
		if err != nil {
			return nil, err
		}
		return sshproxy.NewSSHInstance(client), nil
	}
	orchestrator.SetInstanceFactory(instanceFactory)

	// On-demand browser bridge. Wired before the tunnel reconciler so the CDP
	// dial provider returns a usable closure for non-legacy instances during
	// the very first reconcile pass.
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
		tunnelMgr.SetCDPHealthProbe(bridge.IsCDPReady)

		// Refresh the CDP tunnel status immediately when the browser session
		// transitions running ↔ stopped, so the badge flips green/gray
		// without waiting for the next 60 s periodic health probe.
		refreshCDP := func(instanceID uint) {
			go func() {
				if err := tunnelMgr.CheckTunnelHealth(instanceID, "CDP"); err != nil {
					log.Printf("CDP status refresh for instance %d: %v", instanceID, err)
				}
			}()
		}
		bridge.SetOnSessionStateChanged(refreshCDP)
		handlers.OnBrowserStateChanged = refreshCDP
	}

	// Start background tunnel manager to maintain SSH tunnels for running instances
	if orch := orchestrator.Get(); orch != nil {
		tunnelMgr.StartBackgroundManager(ctx, func(ctx context.Context) ([]uint, error) {
			// Include instances that are stuck in transient/failed states. The
			// per-instance exponential backoff in the tunnel manager keeps this
			// cheap for genuinely dead pods, while ensuring a healthy pod whose
			// DB row was left at 'error' or 'restarting' (e.g. after a failed
			// UpdateImage) is still eligible for tunnel reconciliation.
			var instances []database.Instance
			if err := database.DB.Where("status IN ?", []string{"running", "restarting", "error"}).Find(&instances).Error; err != nil {
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

	// Wire moderator service (Kanban auto-routing). All ports are adapters
	// from the modwiring package so the moderator package itself stays free
	// of claworc-internal imports.
	{
		artifactsDir := config.Cfg.DataPath + "/kanban/artifacts"
		_ = os.MkdirAll(artifactsDir, 0o755)
		modSettings := &modwiring.Settings{DB: database.DB, DefaultDir: artifactsDir}
		handlers.ModeratorSvc = moderator.New(moderator.Options{
			Dialer:    &modwiring.GatewayDialer{DB: database.DB, Tunnels: tunnelMgr},
			Workspace: &modwiring.WorkspaceFS{DB: database.DB, SSH: sshMgr},
			LLM:       &modwiring.LLMClient{DB: database.DB},
			Store:     &modwiring.Store{DB: database.DB},
			Settings:  modSettings,
			Instances: &modwiring.InstanceLister{DB: database.DB},
		})
		handlers.ModeratorSvc.StartSummarizer(ctx)
	}

	// Start background SSH key rotation job (checks daily)
	cancelRotation := handlers.StartKeyRotationJob(ctx)
	_ = cancelRotation // stopped via context cancellation on shutdown

	// Start background backup schedule executor (checks every minute)
	cancelScheduler := backup.StartScheduleExecutor(ctx)
	_ = cancelScheduler // stopped via context cancellation on shutdown

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)

	// Health (no auth)
	r.Get("/health", handlers.HealthCheck)

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// Auth endpoints (no auth required)
		r.Post("/auth/login", handlers.Login)
		r.Get("/auth/setup-required", handlers.SetupRequired)
		r.Post("/auth/setup", handlers.SetupCreateAdmin)
		r.Post("/auth/webauthn/login/begin", handlers.WebAuthnLoginBegin)
		r.Post("/auth/webauthn/login/finish", handlers.WebAuthnLoginFinish)

		// Auth endpoints (auth required)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(sessionStore))

			r.Post("/auth/logout", handlers.Logout)
			r.Get("/auth/me", handlers.GetCurrentUser)
			r.Post("/auth/change-password", handlers.ChangePassword)
			r.Post("/auth/webauthn/register/begin", handlers.WebAuthnRegisterBegin)
			r.Post("/auth/webauthn/register/finish", handlers.WebAuthnRegisterFinish)
			r.Get("/auth/webauthn/credentials", handlers.ListWebAuthnCredentials)
			r.Delete("/auth/webauthn/credentials/{credId}", handlers.DeleteWebAuthnCredential)
		})

		// Protected routes (require auth)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireAuth(sessionStore))

			// Tasks (long-running goroutine registry)
			r.Get("/tasks", handlers.ListTasks)
			r.Get("/tasks/events", handlers.StreamTaskEvents)
			r.Get("/tasks/{id}", handlers.GetTask)
			r.Post("/tasks/{id}/cancel", handlers.CancelTask)

			// Instances (ListInstances filters by role internally)
			r.Get("/instances", handlers.ListInstances)
			r.Put("/instances/reorder", handlers.ReorderInstances)
			r.Get("/instances/{id}", handlers.GetInstance)
			r.Put("/instances/{id}", handlers.UpdateInstance)
			r.Post("/instances/{id}/start", handlers.StartInstance)
			r.Post("/instances/{id}/stop", handlers.StopInstance)
			r.Post("/instances/{id}/restart", handlers.RestartInstance)
			r.Get("/instances/{id}/config", handlers.GetInstanceConfig)
			r.Put("/instances/{id}/config", handlers.UpdateInstanceConfig)
			r.Get("/instances/{id}/logs", handlers.StreamLogs)
			r.Get("/instances/{id}/ssh-test", handlers.SSHConnectionTest)
			r.Get("/instances/{id}/ssh-status", handlers.GetSSHStatus)
			r.Get("/instances/{id}/ssh-events", handlers.GetSSHEvents)
			r.Post("/instances/{id}/ssh-reconnect", handlers.SSHReconnect)
			r.Get("/instances/{id}/tunnels", handlers.GetTunnelStatus)
			r.Get("/instances/{id}/stats", handlers.GetInstanceStats)
			r.Get("/instances/{id}/providers", handlers.ListInstanceProviders)
			r.Post("/instances/{id}/update-image", handlers.UpdateInstanceImage)
			r.Get("/ssh-fingerprint", handlers.GetSSHFingerprint)

			// Files
			r.Get("/instances/{id}/files/browse", handlers.BrowseFiles)
			r.Get("/instances/{id}/files/read", handlers.ReadFileContent)
			r.Get("/instances/{id}/files/download", handlers.DownloadFile)
			r.Post("/instances/{id}/files/create", handlers.CreateNewFile)
			r.Post("/instances/{id}/files/mkdir", handlers.CreateDirectory)
			r.Post("/instances/{id}/files/upload", handlers.UploadFile)
			r.Post("/instances/{id}/files/upload-directory", handlers.UploadDirectory)
			r.Delete("/instances/{id}/files", handlers.DeleteFile)
			r.Post("/instances/{id}/files/rename", handlers.RenameFile)
			r.Post("/instances/{id}/files/copy", handlers.CopyFile)
			r.Get("/instances/{id}/files/search", handlers.SearchFiles)

			// Chat WebSocket
			r.Get("/instances/{id}/chat", handlers.ChatProxy)

			// Terminal WebSocket and session management
			r.Get("/instances/{id}/terminal", handlers.TerminalWSProxy)
			r.Get("/instances/{id}/terminal/sessions", handlers.ListTerminalSessions)
			r.Delete("/instances/{id}/terminal/sessions/{sessionId}", handlers.CloseTerminalSession)

			// Desktop proxy (noVNC/websockify)
			r.HandleFunc("/instances/{id}/desktop/*", handlers.DesktopProxy)

			// On-demand browser pod controls (status / start / stop / migrate).
			r.Get("/instances/{id}/browser/status", handlers.BrowserStatus)
			r.Post("/instances/{id}/browser/start", handlers.BrowserStart)
			r.Post("/instances/{id}/browser/stop", handlers.BrowserStop)
			r.Post("/instances/{id}/browser/migrate", handlers.BrowserMigrate)
			r.Patch("/instances/{id}/browser-active", handlers.SetBrowserActive)

			// Kanban
			r.Get("/kanban/boards", handlers.ListKanbanBoards)
			r.Post("/kanban/boards", handlers.CreateKanbanBoard)
			r.Get("/kanban/boards/{id}", handlers.GetKanbanBoard)
			r.Put("/kanban/boards/{id}", handlers.UpdateKanbanBoard)
			r.Delete("/kanban/boards/{id}", handlers.DeleteKanbanBoard)
			r.Post("/kanban/boards/{id}/tasks", handlers.CreateKanbanTask)
			r.Get("/kanban/tasks/{id}", handlers.GetKanbanTask)
			r.Patch("/kanban/tasks/{id}", handlers.PatchKanbanTask)
			r.Delete("/kanban/tasks/{id}", handlers.DeleteKanbanTask)
			r.Post("/kanban/tasks/{id}/start", handlers.StartKanbanTask)
			r.Post("/kanban/tasks/{id}/stop", handlers.StopKanbanTask)
			r.Post("/kanban/tasks/{id}/comments", handlers.CreateKanbanUserComment)
			r.Post("/kanban/tasks/{id}/reopen", handlers.ReopenKanbanTask)
			r.Get("/kanban/tasks/{id}/artifacts/{artifact_id}", handlers.DownloadKanbanArtifact)

			// Shared Folders
			r.Get("/shared-folders", handlers.ListSharedFolders)
			r.Post("/shared-folders", handlers.CreateSharedFolder)
			r.Get("/shared-folders/{id}", handlers.GetSharedFolder)
			r.Put("/shared-folders/{id}", handlers.UpdateSharedFolder)
			r.Delete("/shared-folders/{id}", handlers.DeleteSharedFolder)

			// Backups — per-handler CanAccessInstance check restricts non-admins
			// to backups for instances they're assigned to.
			r.Post("/instances/{id}/backups", handlers.CreateBackup)
			r.Get("/instances/{id}/backups", handlers.ListInstanceBackups)
			r.Get("/backups", handlers.ListAllBackups)
			r.Get("/backups/{backupId}", handlers.GetBackupDetail)
			r.Delete("/backups/{backupId}", handlers.DeleteBackupHandler)
			r.Post("/backups/{backupId}/cancel", handlers.CancelBackupHandler)
			r.Get("/backups/{backupId}/download", handlers.DownloadBackup)

			// Backup Schedules — handlers filter/authorize by assigned instances.
			r.Post("/backup-schedules", handlers.CreateBackupSchedule)
			r.Get("/backup-schedules", handlers.ListBackupSchedules)
			r.Put("/backup-schedules/{id}", handlers.UpdateBackupSchedule)
			r.Delete("/backup-schedules/{id}", handlers.DeleteBackupSchedule)

			// Instance creators (admin or users with can_create_instances=true).
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireInstanceCreator)

				r.Post("/instances", handlers.CreateInstance)
				r.Post("/instances/{id}/clone", handlers.CloneInstance)
				r.Post("/backups/{backupId}/restore", handlers.RestoreBackupHandler)
			})

			// Admin-only routes
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdmin)

				r.Delete("/instances/{id}", handlers.DeleteInstance)

				// Settings
				r.Get("/settings", handlers.GetSettings)
				r.Put("/settings", handlers.UpdateSettings)
				r.Post("/settings/rotate-ssh-key", handlers.RotateSSHKey)
				r.Get("/audit-logs", handlers.GetAuditLogs)

				// LLM gateway providers and usage
				r.Post("/llm/providers/test", handlers.TestProviderKey)
				r.Post("/llm/providers/sync", handlers.SyncAllProviderModels)
				r.Get("/llm/providers", handlers.ListProviders)
				r.Post("/llm/providers", handlers.CreateProvider)
				r.Put("/llm/providers/{id}", handlers.UpdateProvider)
				r.Delete("/llm/providers/{id}", handlers.DeleteProvider)
				r.Post("/llm/providers/{id}/sync", handlers.SyncProviderModels)
				r.Get("/llm/usage", handlers.GetUsageLogs)
				r.Delete("/llm/usage", handlers.ResetUsageLogs)
				r.Get("/llm/usage/stats", handlers.GetUsageStats)

				// Provider catalog proxy (claworc.com/providers, cached 1h)
				r.Get("/llm/catalog", handlers.GetCatalogProviders)
				r.Get("/llm/catalog/{key}", handlers.GetCatalogProviderDetail)

				// Skills
				r.Get("/skills", handlers.ListSkills)
				r.Post("/skills", handlers.UploadSkill)
				r.Delete("/skills/{slug}", handlers.DeleteSkill)
				r.Get("/skills/clawhub/search", handlers.ClawhubSearch)
				r.Post("/skills/{slug}/deploy", handlers.DeploySkill)
				r.Get("/skills/{slug}/files", handlers.ListSkillFiles)
				r.Get("/skills/{slug}/files/*", handlers.GetSkillFile)
				r.Put("/skills/{slug}/files/*", handlers.PutSkillFile)

				// User management
				r.Get("/users", handlers.ListUsers)
				r.Post("/users", handlers.CreateUser)
				r.Delete("/users/{userId}", handlers.DeleteUser)
				r.Put("/users/{userId}/role", handlers.UpdateUserRole)
				r.Put("/users/{userId}/permissions", handlers.UpdateUserPermissions)
				r.Get("/users/{userId}/instances", handlers.GetUserAssignedInstances)
				r.Put("/users/{userId}/instances", handlers.SetUserAssignedInstances)
				r.Post("/users/{userId}/reset-password", handlers.ResetUserPassword)
			})
		})
	})

	// OpenClaw control proxy (top-level, outside /api/v1/)
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth(sessionStore))
		r.HandleFunc("/openclaw/{id}/*", handlers.ControlProxy)
	})

	// SPA static files (embedded)
	distFS, _ := fs.Sub(frontendFS, "frontend/dist")
	spa := middleware.NewSPAHandler(distFS)
	r.NotFound(spa.ServeHTTP)

	// Graceful shutdown
	srv := &http.Server{
		Addr:    ":8000",
		Handler: r,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("Server starting on :8000")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-sigCtx.Done()
	log.Println("Shutting down...")

	termMgr.Stop()
	tunnelMgr.StopAll()

	if err := sshMgr.CloseAll(); err != nil {
		log.Printf("SSH manager shutdown: %v", err)
	}

	taskMgr.Close()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}
	log.Println("Server stopped")
}

func runCLICommand(command string) {
	fs := flag.NewFlagSet(command, flag.ExitOnError)
	username := fs.String("username", "", "Username")
	password := fs.String("password", "", "Password")
	fs.Parse(os.Args[2:])

	if *username == "" || *password == "" {
		fmt.Fprintf(os.Stderr, "Usage: claworc --%s --username <user> --password <pass>\n", command)
		os.Exit(1)
	}

	config.Load()
	if err := database.Init(); err != nil {
		log.Fatalf("Database init: %v", err)
	}
	defer database.Close()

	hash, err := auth.HashPassword(*password)
	if err != nil {
		log.Fatalf("Failed to hash password: %v", err)
	}

	switch command {
	case "create-admin":
		if existing, _ := database.GetUserByUsername(*username); existing != nil {
			if err := database.UpdateUserPassword(existing.ID, hash); err != nil {
				log.Fatalf("Failed to update admin password: %v", err)
			}
			fmt.Printf("Admin user '%s' already exists — password updated.\n", *username)
		} else {
			user := &database.User{
				Username:     *username,
				PasswordHash: hash,
				Role:         "admin",
			}
			if err := database.CreateUser(user); err != nil {
				log.Fatalf("Failed to create admin: %v", err)
			}
			fmt.Printf("Admin user '%s' created successfully.\n", *username)
		}

	case "reset-password":
		user, err := database.GetUserByUsername(*username)
		if err != nil {
			log.Fatalf("User '%s' not found", *username)
		}
		if err := database.UpdateUserPassword(user.ID, hash); err != nil {
			log.Fatalf("Failed to update password: %v", err)
		}
		fmt.Printf("Password reset for '%s'. Note: existing sessions will expire within 1 hour.\n", *username)
	}
}
