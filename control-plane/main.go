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
	"syscall"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/auth"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/handlers"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/sshterminal"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

//go:embed frontend/dist
var frontendFS embed.FS

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

	ctx := context.Background()
	if err := orchestrator.InitOrchestrator(ctx); err != nil {
		log.Printf("WARNING: %v", err)
	}

	// Configure SSH manager with orchestrator for automatic reconnection
	if orch := orchestrator.Get(); orch != nil {
		sshMgr.SetOrchestrator(orch)
	}
	sshMgr.StartHealthChecker(ctx)

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
			r.Get("/instances/{id}/tunnels", handlers.GetTunnelStatus)

			// Files
			r.Get("/instances/{id}/files/browse", handlers.BrowseFiles)
			r.Get("/instances/{id}/files/read", handlers.ReadFileContent)
			r.Get("/instances/{id}/files/download", handlers.DownloadFile)
			r.Post("/instances/{id}/files/create", handlers.CreateNewFile)
			r.Post("/instances/{id}/files/mkdir", handlers.CreateDirectory)
			r.Post("/instances/{id}/files/upload", handlers.UploadFile)

			// Chat WebSocket
			r.Get("/instances/{id}/chat", handlers.ChatProxy)

			// Terminal WebSocket and session management
			r.Get("/instances/{id}/terminal", handlers.TerminalWSProxy)
			r.Get("/instances/{id}/terminal/sessions", handlers.ListTerminalSessions)
			r.Delete("/instances/{id}/terminal/sessions/{sessionId}", handlers.CloseTerminalSession)

			// Desktop proxy (Selkies streaming UI)
			r.HandleFunc("/instances/{id}/desktop/*", handlers.DesktopProxy)

			// Control proxy
			r.Get("/instances/{id}/control/*", handlers.ControlProxy)

			// Admin-only routes
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireAdmin)

				r.Post("/instances", handlers.CreateInstance)
				r.Post("/instances/{id}/clone", handlers.CloneInstance)
				r.Delete("/instances/{id}", handlers.DeleteInstance)

				// Settings
				r.Get("/settings", handlers.GetSettings)
				r.Put("/settings", handlers.UpdateSettings)

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
		user := &database.User{
			Username:     *username,
			PasswordHash: hash,
			Role:         "admin",
		}
		if err := database.CreateUser(user); err != nil {
			log.Fatalf("Failed to create admin: %v", err)
		}
		fmt.Printf("Admin user '%s' created successfully.\n", *username)

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
