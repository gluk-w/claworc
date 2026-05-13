package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

var BuildDate string

var startTime = time.Now()

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

	uptime := time.Since(startTime)
	resp := map[string]any{
		"status":               status,
		"orchestrator":         orchStatus,
		"orchestrator_backend": orchBackend,
		"orchestrator_status":  orchestrator.Status(),
		"database":             dbStatus,
		"uptime":               fmt.Sprintf("%.0fs", uptime.Seconds()),
	}
	if BuildDate != "" {
		resp["build_date"] = BuildDate
	}
	writeJSON(w, http.StatusOK, resp)
}
