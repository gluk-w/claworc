package orchestrator

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

var (
	current ContainerOrchestrator
	mu      sync.RWMutex
)

func InitOrchestrator(ctx context.Context) error {
	backend, err := database.GetSetting("orchestrator_backend")
	if err != nil {
		backend = "auto"
	}

	if backend == "auto" || backend == "kubernetes" {
		k8s := &KubernetesOrchestrator{}
		if err := k8s.Initialize(ctx); err == nil && k8s.IsAvailable(ctx) {
			mu.Lock()
			current = k8s
			mu.Unlock()
			log.Println("Orchestrator: using Kubernetes backend")
			if backend == "auto" {
				_ = database.SetSetting("orchestrator_backend", "kubernetes")
			}
			return nil
		} else if err != nil {
			log.Printf("Kubernetes backend unavailable: %v", err)
		}
	}

	if backend == "auto" || backend == "docker" {
		docker := &DockerOrchestrator{}
		if err := docker.Initialize(ctx); err == nil && docker.IsAvailable(ctx) {
			mu.Lock()
			current = docker
			mu.Unlock()
			log.Println("Orchestrator: using Docker backend")
			if backend == "auto" {
				_ = database.SetSetting("orchestrator_backend", "docker")
			}
			return nil
		} else if err != nil {
			log.Printf("Docker backend unavailable: %v", err)
		}
	}

	log.Println("WARNING: No orchestrator backend available")
	return fmt.Errorf("no orchestrator backend available (tried: %s)", backend)
}

func Get() ContainerOrchestrator {
	mu.RLock()
	defer mu.RUnlock()
	return current
}
