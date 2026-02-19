package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gluk-w/claworc/llm-proxy/internal/api"
	"github.com/gluk-w/claworc/llm-proxy/internal/config"
	"github.com/gluk-w/claworc/llm-proxy/internal/database"
	"github.com/gluk-w/claworc/llm-proxy/internal/providers"
	"github.com/gluk-w/claworc/llm-proxy/internal/proxy"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

func main() {
	config.Load()

	if err := database.Init(); err != nil {
		log.Fatalf("Database init: %v", err)
	}
	defer database.Close()

	// Apply custom upstream URLs for local providers
	if config.Cfg.OllamaURL != "" {
		providers.SetCustomUpstream("ollama", config.Cfg.OllamaURL)
	}
	if config.Cfg.LlamaCppURL != "" {
		providers.SetCustomUpstream("llamacpp", config.Cfg.LlamaCppURL)
	}

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)

	// Health (no auth)
	r.Get("/health", api.HealthCheck)

	// Agent-facing LLM proxy routes
	r.Route("/v1/{provider}", func(r chi.Router) {
		r.Use(proxy.AuthMiddleware)
		r.Use(proxy.BudgetMiddleware)
		r.Use(proxy.RateLimitMiddleware)
		r.HandleFunc("/*", proxy.ProxyHandler)
	})

	// Management API (control-plane-facing)
	r.Route("/admin", func(r chi.Router) {
		r.Use(api.AdminAuth)

		r.Post("/tokens", api.RegisterToken)
		r.Delete("/tokens/{name}", api.RevokeToken)
		r.Put("/tokens/{name}/disable", api.DisableToken)
		r.Put("/tokens/{name}/enable", api.EnableToken)
		r.Put("/keys", api.SyncKeys)
		r.Get("/usage", api.GetUsage)
		r.Get("/usage/instances/{name}", api.GetInstanceUsage)
		r.Get("/limits/{name}", api.GetLimits)
		r.Put("/limits/{name}", api.SetLimits)
	})

	// Graceful shutdown
	srv := &http.Server{
		Addr:    config.Cfg.ListenAddr,
		Handler: r,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("LLM Proxy starting on %s", config.Cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-sigCtx.Done()
	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown error: %v", err)
	}
	log.Println("LLM Proxy stopped")
}
