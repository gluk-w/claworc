package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/handlers"
	"github.com/gluk-w/claworc/control-plane/internal/llmgateway"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
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

	// Start background tunnel manager to maintain SSH tunnels for running instances
	if orch := orchestrator.Get(); orch != nil {
		tunnelMgr.StartBackgroundManager(ctx, func(ctx context.Context) ([]uint, error) {
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

	// Start background SSH key rotation job (checks daily)
	cancelRotation := handlers.StartKeyRotationJob(ctx)
	_ = cancelRotation // stopped via context cancellation on shutdown

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
			r.Get("/ssh-fingerprint", handlers.GetSSHFingerprint)

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

			// Chat WebSocket
			r.Get("/instances/{id}/chat", handlers.ChatProxy)

			// Terminal WebSocket and session management
			r.Get("/instances/{id}/terminal", handlers.TerminalWSProxy)
			r.Get("/instances/{id}/terminal/sessions", handlers.ListTerminalSessions)
			r.Delete("/instances/{id}/terminal/sessions/{sessionId}", handlers.CloseTerminalSession)

			// Desktop proxy (noVNC/websockify)
			r.HandleFunc("/instances/{id}/desktop/*", handlers.DesktopProxy)

			// Control proxy
			r.HandleFunc("/instances/{id}/control/*", handlers.ControlProxy)

			// Admin-only routes
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdmin)

				r.Post("/instances", handlers.CreateInstance)
				r.Post("/instances/{id}/clone", handlers.CloneInstance)
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

				// User management
				r.Get("/users", handlers.ListUsers)
				r.Post("/users", handlers.CreateUser)
				r.Delete("/users/{userId}", handlers.DeleteUser)
				r.Put("/users/{userId}/role", handlers.UpdateUserRole)
				r.Get("/users/{userId}/instances", handlers.GetUserAssignedInstances)
				r.Put("/users/{userId}/instances", handlers.SetUserAssignedInstances)
				r.Post("/users/{userId}/reset-password", handlers.ResetUserPassword)
			})
		})
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
