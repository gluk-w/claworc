package handlers

import (
	"net/http"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// GetOrchestratorStatus returns the current init status snapshot.
func GetOrchestratorStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, orchestrator.Status())
}

// ReinitializeOrchestrator re-runs InitOrchestrator and returns the new status.
// Lets operators recover after fixing Docker/Kubernetes availability without
// restarting the control plane.
func ReinitializeOrchestrator(w http.ResponseWriter, r *http.Request) {
	_ = orchestrator.InitOrchestrator(r.Context())
	writeJSON(w, http.StatusOK, orchestrator.Status())
}
