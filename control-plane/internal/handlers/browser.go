package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

// browserStatusResponse is the JSON shape used by GET /browser/status. The
// frontend desktop tab polls this to render a "starting browser" loading
// state.
type browserStatusResponse struct {
	State            string     `json:"state"` // stopped|starting|running|error|legacy|disabled
	IsLegacyEmbedded bool       `json:"is_legacy_embedded"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	ErrorMsg         string     `json:"error_msg,omitempty"`
}

// BrowserStatus reports the current state of the browser pod for an
// instance. For legacy instances state="legacy"; for non-legacy with no
// session row state="stopped".
func BrowserStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	var inst database.Instance
	if err := database.DB.First(&inst, uint(id)).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}
	if database.IsLegacyEmbedded(inst.ContainerImage) {
		writeJSON(w, http.StatusOK, browserStatusResponse{State: "legacy", IsLegacyEmbedded: true})
		return
	}
	resp := browserStatusResponse{State: "stopped"}
	if BrowserBridgeRef == nil {
		resp.State = "disabled"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	row, err := database.GetBrowserSession(uint(id))
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		resp.State = row.Status
		resp.ErrorMsg = row.ErrorMsg
		started := row.StartedAt
		last := row.LastUsedAt
		if !started.IsZero() {
			resp.StartedAt = &started
		}
		if !last.IsZero() {
			resp.LastUsedAt = &last
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// BrowserStart asks the bridge to ensure the browser session is running and
// returns the resulting status. Useful for explicit user-triggered spawns
// from the desktop tab.
func BrowserStart(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	if BrowserBridgeRef == nil {
		writeError(w, http.StatusServiceUnavailable, "browser bridge not configured")
		return
	}
	user := middleware.GetUser(r)
	var userID uint
	if user != nil {
		userID = user.ID
	}
	if err := BrowserBridgeRef.EnsureSession(r.Context(), uint(id), userID); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	BrowserStatus(w, r)
}

// BrowserStop tears down the browser session for an instance immediately
// without waiting for the idle reaper.
func BrowserStop(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	if BrowserStopper == nil {
		writeError(w, http.StatusServiceUnavailable, "browser bridge not configured")
		return
	}
	if err := BrowserStopper.StopSession(r.Context(), uint(id)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = database.UpdateBrowserSessionStatus(uint(id), "stopped", "")
	if OnBrowserStateChanged != nil {
		OnBrowserStateChanged(uint(id))
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "stopped"})
}

// BrowserMigrate kicks off the legacy → on-demand migration for an instance.
// It runs as a TaskBrowserMigrate task so progress is surfaced via toasts and
// the user can cancel it; the task ID is returned so the frontend can
// subscribe via the existing tasks SSE stream.
func BrowserMigrate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if !middleware.CanAccessInstance(r, uint(id)) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}
	user := middleware.GetUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if BrowserMigrator == nil {
		writeError(w, http.StatusServiceUnavailable, "browser migration not configured")
		return
	}
	taskID, err := BrowserMigrator.Migrate(r.Context(), uint(id), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": taskID})
}

// BrowserStopper, BrowserMigrator, and BrowserAdmin are wired in main.go so
// handlers stay independent of the concrete browserprov implementation.
var (
	BrowserStopper  BrowserSessionStopper
	BrowserMigrator BrowserMigrationRunner
	BrowserAdmin    BrowserAdminOps

	// OnBrowserStateChanged, when set, is invoked after a successful
	// BrowserStart / BrowserStop so the SSH tunnel manager can refresh the
	// CDP tunnel status immediately rather than waiting for the next 60 s
	// periodic health probe.
	OnBrowserStateChanged func(instanceID uint)
)

// BrowserSessionStopper is the contract for force-stopping a browser session.
type BrowserSessionStopper interface {
	StopSession(ctx context.Context, instanceID uint) error
}

// BrowserMigrationRunner kicks off an asynchronous legacy → external
// migration and returns the TaskManager task ID.
type BrowserMigrationRunner interface {
	Migrate(ctx context.Context, instanceID, userID uint) (taskID string, err error)
}

// BrowserAdminOps groups the browser-pod admin operations that instance CRUD
// handlers (delete, clone-cancel, clone) reach for outside the bridge's
// session lifecycle.
type BrowserAdminOps interface {
	DeleteBrowserPod(ctx context.Context, instanceID uint) error
	CloneBrowserVolume(ctx context.Context, srcInstanceName, dstInstanceName string) error
}
