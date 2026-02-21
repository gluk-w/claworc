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
	"github.com/gluk-w/claworc/control-plane/internal/logutil"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshkeys"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
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

	// Initialize SSH tunnel subsystem
	sshtunnel.InitGlobal()
	log.Println("SSH tunnel manager initialized")

	// Initialize SSH audit subsystem
	sshaudit.InitGlobal(database.DB, sshaudit.DefaultRetentionDays)
	log.Println("SSH audit logger initialized")

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

			// Global SSH dashboard (access-filtered internally like ListInstances)
			r.Get("/ssh-status", handlers.GetGlobalSSHStatus)
			r.Get("/ssh-metrics", handlers.GetSSHMetrics)

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
			r.Get("/instances/{id}/tunnels", handlers.GetTunnelStatus)
			r.Get("/instances/{id}/ssh-status", handlers.GetSSHStatus)
			r.Get("/instances/{id}/ssh-events", handlers.GetSSHEvents)
			r.Post("/instances/{id}/ssh-test", handlers.SSHConnectionTest)
			r.Post("/instances/{id}/ssh-reconnect", handlers.SSHReconnect)
			r.Get("/instances/{id}/ssh-fingerprint", handlers.GetSSHFingerprint)
			r.Get("/instances/{id}/logs", handlers.StreamLogs)

			// Files
			r.Get("/instances/{id}/files/browse", handlers.BrowseFiles)
			r.Get("/instances/{id}/files/read", handlers.ReadFileContent)
			r.Get("/instances/{id}/files/download", handlers.DownloadFile)
			r.Post("/instances/{id}/files/create", handlers.CreateNewFile)
			r.Post("/instances/{id}/files/mkdir", handlers.CreateDirectory)
			r.Post("/instances/{id}/files/upload", handlers.UploadFile)

			// Chat WebSocket
			r.Get("/instances/{id}/chat", handlers.ChatProxy)

			// Terminal WebSocket (supports ?session_id= for reconnection)
			r.Get("/instances/{id}/terminal", handlers.TerminalWSProxy)

			// Terminal session management
			r.Get("/instances/{id}/terminal/sessions", handlers.ListTerminalSessions)
			r.Delete("/instances/{id}/terminal/sessions/{sessionId}", handlers.DeleteTerminalSession)
			r.Get("/instances/{id}/terminal/sessions/{sessionId}/recording", handlers.GetTerminalRecording)

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
				r.Post("/instances/{id}/rotate-ssh-key", handlers.RotateSSHKey)

				// SSH audit logs
				r.Get("/ssh-audit-logs", handlers.GetSSHAuditLogs)
				r.Post("/ssh-audit-logs/purge", handlers.PurgeSSHAuditLogs)

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

	// Start background tunnel maintenance goroutine
	go tunnelMaintenanceLoop(sigCtx)

	// Start background key rotation checker (runs daily)
	go keyRotationLoop(sigCtx)

	// Start background audit log retention purge (runs daily)
	go auditRetentionLoop(sigCtx)

	go func() {
		log.Printf("Server starting on :8000")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-sigCtx.Done()
	log.Println("Shutting down...")

	// Shut down SSH tunnels and connections
	if tm := sshtunnel.GetTunnelManager(); tm != nil {
		tm.Shutdown()
	}
	if sm := sshtunnel.GetSSHManager(); sm != nil {
		if err := sm.CloseAll(); err != nil {
			log.Printf("Error closing SSH connections: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}
	log.Println("Server stopped")
}

// tunnelMaintenanceLoop periodically checks running instances and ensures
// tunnels are established for those with SSH connections, and cleans up
// tunnels for stopped or deleted instances.
func tunnelMaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			maintainTunnels(ctx)
		}
	}
}

func maintainTunnels(ctx context.Context) {
	orch := orchestrator.Get()
	tm := sshtunnel.GetTunnelManager()
	sm := sshtunnel.GetSSHManager()
	if orch == nil || tm == nil || sm == nil {
		return
	}

	// List all instances from the database
	var instances []database.Instance
	if err := database.DB.Find(&instances).Error; err != nil {
		log.Printf("[tunnel-maint] failed to list instances: %v", err)
		return
	}

	// Build a set of running instance names
	runningInstances := make(map[string]bool)
	for _, inst := range instances {
		status, _ := orch.GetInstanceStatus(ctx, inst.Name)
		if status == "running" {
			runningInstances[inst.Name] = true
		}
	}

	// Ensure tunnels exist for running instances that have SSH connections
	for name := range runningInstances {
		if sm.HasClient(name) {
			tunnels := tm.GetTunnels(name)
			if len(tunnels) == 0 {
				log.Printf("[tunnel-maint] creating tunnels for running instance %s", name)
				if err := tm.StartTunnelsForInstance(ctx, name); err != nil {
					log.Printf("[tunnel-maint] failed to start tunnels for %s: %v", name, err)
				}
			}
		}
	}

	// Remove tunnels for stopped/deleted instances
	allTunnels := tm.GetAllTunnels()
	for name := range allTunnels {
		if !runningInstances[name] {
			log.Printf("[tunnel-maint] removing tunnels for non-running instance %s", name)
			tm.StopTunnelsForInstance(name)
		}
	}

	// Log tunnel status for observability
	allTunnels = tm.GetAllTunnels()
	totalTunnels := 0
	for _, tunnels := range allTunnels {
		totalTunnels += len(tunnels)
	}
	if totalTunnels > 0 {
		log.Printf("[tunnel-maint] active: %d tunnel(s) across %d instance(s)", totalTunnels, len(allTunnels))
	}
}

// keyRotationLoop runs a daily check for instances whose SSH keys need rotation
// based on each instance's KeyRotationPolicy.
func keyRotationLoop(ctx context.Context) {
	// Run an initial check shortly after startup (wait 1 minute for services to initialize)
	select {
	case <-ctx.Done():
		return
	case <-time.After(1 * time.Minute):
		checkKeyRotations(ctx)
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkKeyRotations(ctx)
		}
	}
}

// checkKeyRotations inspects all instances and automatically rotates SSH keys
// for those whose keys are older than their rotation policy threshold.
func checkKeyRotations(ctx context.Context) {
	var instances []database.Instance
	if err := database.DB.Find(&instances).Error; err != nil {
		log.Printf("[key-rotation] failed to list instances: %v", err)
		return
	}

	orch := orchestrator.Get()
	sm := sshtunnel.GetSSHManager()
	if orch == nil || sm == nil {
		return
	}

	now := time.Now()
	rotated := 0

	for _, inst := range instances {
		// Skip instances with rotation disabled (policy = 0)
		if inst.KeyRotationPolicy <= 0 {
			continue
		}

		// Skip instances without SSH keys configured
		if inst.SSHPublicKey == "" || inst.SSHPrivateKeyPath == "" {
			continue
		}

		// Determine if rotation is due
		var lastRotation time.Time
		if inst.LastKeyRotation != nil {
			lastRotation = *inst.LastKeyRotation
		} else {
			// Never rotated - use creation time as baseline
			lastRotation = inst.CreatedAt
		}

		daysSinceRotation := now.Sub(lastRotation).Hours() / 24
		if daysSinceRotation < float64(inst.KeyRotationPolicy) {
			continue
		}

		// Check that instance is running
		status, _ := orch.GetInstanceStatus(ctx, inst.Name)
		if status != "running" {
			log.Printf("[key-rotation] skipping %s: instance not running (status: %s)",
				logutil.SanitizeForLog(inst.Name), status)
			continue
		}

		// Get SSH client
		sshClient, err := sm.GetClient(inst.Name)
		if err != nil {
			log.Printf("[key-rotation] skipping %s: no active SSH connection",
				logutil.SanitizeForLog(inst.Name))
			continue
		}

		// Get SSH endpoint
		host, port, err := orch.GetInstanceSSHEndpoint(ctx, inst.Name)
		if err != nil {
			log.Printf("[key-rotation] skipping %s: failed to get SSH endpoint: %v",
				logutil.SanitizeForLog(inst.Name), err)
			continue
		}

		log.Printf("[key-rotation] rotating key for %s (%.0f days since last rotation, policy: %d days)",
			logutil.SanitizeForLog(inst.Name), daysSinceRotation, inst.KeyRotationPolicy)

		// Perform rotation
		newPubKey, newKeyPath, result, err := sshkeys.RotateKeyPair(
			sshClient,
			inst.Name,
			inst.SSHPublicKey,
			host,
			port,
		)
		if err != nil {
			log.Printf("[key-rotation] FAILED for %s: %v", logutil.SanitizeForLog(inst.Name), err)
			continue
		}

		// Update database
		rotatedAt := result.RotatedAt
		if dbErr := database.DB.Model(&inst).Updates(map[string]interface{}{
			"ssh_public_key":       string(newPubKey),
			"ssh_private_key_path": newKeyPath,
			"ssh_key_fingerprint":  result.NewFingerprint,
			"last_key_rotation":    &rotatedAt,
		}).Error; dbErr != nil {
			log.Printf("[key-rotation] failed to update database for %s: %v",
				logutil.SanitizeForLog(inst.Name), dbErr)
			continue
		}

		// Reconnect with new key
		if sm.HasClient(inst.Name) {
			_ = sm.Close(inst.Name)
		}
		if _, reconnErr := sm.Connect(ctx, inst.Name, host, port, newKeyPath); reconnErr != nil {
			log.Printf("[key-rotation] reconnection with new key failed for %s: %v",
				logutil.SanitizeForLog(inst.Name), reconnErr)
		}

		// Restart tunnels
		if tm := sshtunnel.GetTunnelManager(); tm != nil {
			tm.StopTunnelsForInstance(inst.Name)
			if tunnelErr := tm.StartTunnelsForInstance(ctx, inst.Name); tunnelErr != nil {
				log.Printf("[key-rotation] tunnel restart failed for %s: %v",
					logutil.SanitizeForLog(inst.Name), tunnelErr)
			}
		}

		// Clean up old key file
		if inst.SSHPrivateKeyPath != newKeyPath {
			if delErr := sshkeys.DeletePrivateKey(inst.SSHPrivateKeyPath); delErr != nil {
				log.Printf("[key-rotation] failed to delete old key file for %s: %v",
					logutil.SanitizeForLog(inst.Name), delErr)
			}
		}

		log.Printf("[key-rotation] completed for %s (fingerprint: %s)",
			logutil.SanitizeForLog(inst.Name), result.NewFingerprint)
		rotated++
	}

	if rotated > 0 {
		log.Printf("[key-rotation] automatic rotation completed: %d instance(s) rotated", rotated)
	}
}

// auditRetentionLoop periodically purges old SSH audit log entries based on
// the configured retention policy.
func auditRetentionLoop(ctx context.Context) {
	// Wait a few minutes after startup before first purge
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}

	auditor := sshaudit.GetAuditor()
	if auditor != nil {
		auditor.PurgeOlderThan(0)
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a := sshaudit.GetAuditor(); a != nil {
				a.PurgeOlderThan(0)
			}
		}
	}
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
