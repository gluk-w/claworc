package orchestrator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
)

// BackendAttempt records one backend's init result for diagnostics.
type BackendAttempt struct {
	Backend string    `json:"backend"`
	OK      bool      `json:"ok"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// InitStatus is a snapshot of the most recent InitOrchestrator run.
type InitStatus struct {
	Backend     string           `json:"backend"`
	Available   bool             `json:"available"`
	LastAttempt time.Time        `json:"last_attempt"`
	Attempts    []BackendAttempt `json:"attempts,omitempty"`
}

var (
	current ContainerOrchestrator
	status  InitStatus
	mu      sync.RWMutex
)

func InitOrchestrator(ctx context.Context) error {
	backend, err := database.GetSetting("orchestrator_backend")
	if err != nil || backend == "" {
		backend = "auto"
	}

	now := time.Now()
	newStatus := InitStatus{
		Backend:     "none",
		Available:   false,
		LastAttempt: now,
	}

	// Validate the setting; invalid values fall back to auto with a recorded note.
	if backend != "auto" && backend != "kubernetes" && backend != "docker" {
		log.Printf("Invalid orchestrator_backend %q, falling back to auto", backend)
		newStatus.Attempts = append(newStatus.Attempts, BackendAttempt{
			Backend: backend,
			OK:      false,
			Reason:  "invalid_setting",
			Message: fmt.Sprintf("invalid orchestrator_backend %q; expected auto, kubernetes, or docker", backend),
			At:      now,
		})
		backend = "auto"
	}

	if backend == "auto" || backend == "kubernetes" {
		k8s := &KubernetesOrchestrator{}
		attempt := BackendAttempt{Backend: "kubernetes", At: time.Now()}
		err := k8s.Initialize(ctx)
		if err == nil && k8s.IsAvailable(ctx) {
			attempt.OK = true
			newStatus.Attempts = append(newStatus.Attempts, attempt)
			newStatus.Backend = "kubernetes"
			newStatus.Available = true
			setCurrent(k8s, newStatus)
			log.Println("Orchestrator: using Kubernetes backend")
			if backend == "auto" {
				_ = database.SetSetting("orchestrator_backend", "kubernetes")
			}
			return nil
		}
		if err != nil {
			attempt.Reason, attempt.Message = classify(err)
			log.Printf("Kubernetes backend unavailable: %v", err)
		} else {
			attempt.Reason = "unavailable"
			attempt.Message = "kubernetes backend reported unavailable"
		}
		newStatus.Attempts = append(newStatus.Attempts, attempt)
	}

	if backend == "auto" || backend == "docker" {
		docker := &DockerOrchestrator{}
		attempt := BackendAttempt{Backend: "docker", At: time.Now()}
		err := docker.Initialize(ctx)
		if err == nil && docker.IsAvailable(ctx) {
			attempt.OK = true
			newStatus.Attempts = append(newStatus.Attempts, attempt)
			newStatus.Backend = "docker"
			newStatus.Available = true
			setCurrent(docker, newStatus)
			log.Println("Orchestrator: using Docker backend")
			if backend == "auto" {
				_ = database.SetSetting("orchestrator_backend", "docker")
			}
			return nil
		}
		if err != nil {
			attempt.Reason, attempt.Message = classify(err)
			log.Printf("Docker backend unavailable: %v", err)
		} else {
			attempt.Reason = "unavailable"
			attempt.Message = "docker backend reported unavailable"
		}
		newStatus.Attempts = append(newStatus.Attempts, attempt)
	}

	setCurrent(nil, newStatus)
	log.Println("WARNING: No orchestrator backend available")
	return fmt.Errorf("no orchestrator backend available (tried: %s)", backend)
}

func setCurrent(o ContainerOrchestrator, s InitStatus) {
	mu.Lock()
	defer mu.Unlock()
	current = o
	status = s
}

// classify maps a wrapped Initialize() error to a stable reason code.
func classify(err error) (reason, message string) {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "docker ping"):
		return "daemon_unreachable", msg
	case strings.HasPrefix(msg, "docker client"):
		return "client_error", msg
	case strings.HasPrefix(msg, "docker network"):
		return "client_error", msg
	case strings.HasPrefix(msg, "k8s config"):
		return "config_missing", msg
	case strings.HasPrefix(msg, "k8s clientset"):
		return "client_error", msg
	case strings.HasPrefix(msg, "k8s namespace check"):
		return "namespace_missing", msg
	default:
		return "unknown", msg
	}
}

func Get() ContainerOrchestrator {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Status returns a snapshot of the most recent init attempt.
func Status() InitStatus {
	mu.RLock()
	defer mu.RUnlock()
	out := status
	if len(status.Attempts) > 0 {
		out.Attempts = append([]BackendAttempt(nil), status.Attempts...)
	}
	return out
}

// Set replaces the current orchestrator. Intended for testing.
func Set(o ContainerOrchestrator) {
	mu.Lock()
	defer mu.Unlock()
	current = o
	if o != nil {
		status = InitStatus{Backend: o.BackendName(), Available: true, LastAttempt: time.Now()}
	} else {
		status = InitStatus{Backend: "none", Available: false, LastAttempt: time.Now()}
	}
}

// SetInstanceFactory configures the InstanceFactory on the active orchestrator.
func SetInstanceFactory(factory sshproxy.InstanceFactory) {
	mu.RLock()
	defer mu.RUnlock()
	switch o := current.(type) {
	case *DockerOrchestrator:
		o.InstanceFactory = factory
	case *KubernetesOrchestrator:
		o.InstanceFactory = factory
	}
}
