package handlers

import (
	"net/http"

	"github.com/glukw/claworc/internal/database"
	"github.com/glukw/claworc/internal/orchestrator"
)

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	dbStatus := "disconnected"
	if database.DB != nil {
		sqlDB, err := database.DB.DB()
		if err == nil {
			if err := sqlDB.Ping(); err == nil {
				dbStatus = "connected"
			}
		}
	}

	orchStatus := "disconnected"
	orchBackend := "none"
	if orch := orchestrator.Get(); orch != nil {
		orchStatus = "connected"
		orchBackend = orch.BackendName()
	}

	status := "healthy"
	if dbStatus != "connected" {
		status = "unhealthy"
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":               status,
		"orchestrator":         orchStatus,
		"orchestrator_backend": orchBackend,
		"database":             dbStatus,
	})
}
