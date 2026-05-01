package handlers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshaudit"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/go-chi/chi/v5"
)

// SSHMgr is set from main.go during init.
var SSHMgr *sshproxy.SSHManager

// TunnelMgr is set from main.go during init.
var TunnelMgr *sshproxy.TunnelManager

// BrowserBridgeRef is set from main.go during init. It is the bridge handlers
// use to ensure on-demand browser sessions and dial CDP/VNC for non-legacy
// instances. The interface here keeps handlers free of an import cycle with
// browserprov; main.go assigns a *browserprov.BrowserBridge.
var BrowserBridgeRef BrowserBridge

// BrowserBridge is the contract handlers depend on. It mirrors
// browserprov.BrowserBridge's exported methods.
type BrowserBridge interface {
	EnsureSession(ctx context.Context, instanceID, userID uint) error
	DialCDP(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error)
	DialVNC(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error)
	VNCDialer(ctx context.Context, instanceID uint) (func(context.Context, string, string) (net.Conn, error), error)
	TestConnection(ctx context.Context, instanceID uint) (string, error)
	Reconnect(ctx context.Context, instanceID uint) error
	Touch(instanceID uint)
}

// SSHConnectionTest tests SSH connectivity to an instance by establishing a
// connection (or reusing an existing one) and executing a simple command.
//
// Query parameter `target` selects which pod to probe:
//   - "" or "agent" (default): the agent pod's sshd, via the global SSHMgr.
//   - "browser": the per-instance browser pod's sshd, via the browser bridge.
//
// Both responses share the same shape; the response echoes `target` so the
// client can disambiguate when fanning out parallel requests.
func SSHConnectionTest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	target := normalizeSSHTarget(r.URL.Query().Get("target"))

	if target == "browser" {
		sshConnectionTestBrowser(w, r, &inst)
		return
	}

	orch := orchestrator.Get()
	if orch == nil {
		writeJSON(w, http.StatusServiceUnavailable, sshTestResponse(target, "error", "", 0, "No orchestrator available"))
		return
	}

	if SSHMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, sshTestResponse(target, "error", "", 0, "SSH manager not initialized"))
		return
	}

	start := time.Now()

	client, err := SSHMgr.EnsureConnectedWithIPCheck(r.Context(), inst.ID, orch, inst.AllowedSourceIPs)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		writeJSON(w, http.StatusOK, sshTestResponse(target, "error", "", latency, err.Error()))
		return
	}

	session, err := client.NewSession()
	if err != nil {
		latency := time.Since(start).Milliseconds()
		writeJSON(w, http.StatusOK, sshTestResponse(target, "error", "", latency, "Failed to create SSH session: "+err.Error()))
		return
	}
	defer session.Close()

	output, err := session.CombinedOutput("echo \"SSH test successful\"")
	latency := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusOK, sshTestResponse(target, "error", string(output), latency, "Command execution failed: "+err.Error()))
		return
	}

	auditLog(sshaudit.EventCommandExec, inst.ID, getUsername(r),
		fmt.Sprintf("command=echo SSH test, latency=%dms, result=success", latency))

	writeJSON(w, http.StatusOK, sshTestResponse(target, "ok", string(output), latency, ""))
}

// sshConnectionTestBrowser runs the SSH connectivity probe against the
// per-instance browser pod via the browser bridge. Refuses for legacy
// instances (no separate browser pod) and when the bridge isn't wired.
func sshConnectionTestBrowser(w http.ResponseWriter, r *http.Request, inst *database.Instance) {
	const target = "browser"
	if database.IsLegacyEmbedded(inst.ContainerImage) {
		writeJSON(w, http.StatusOK, sshTestResponse(target, "error", "", 0,
			"Legacy instance: browser runs inside the agent pod, no separate browser SSH endpoint"))
		return
	}
	if BrowserBridgeRef == nil {
		writeJSON(w, http.StatusServiceUnavailable, sshTestResponse(target, "error", "", 0,
			"Browser bridge not configured"))
		return
	}
	start := time.Now()
	output, err := BrowserBridgeRef.TestConnection(r.Context(), inst.ID)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, sshTestResponse(target, "error", output, latency, err.Error()))
		return
	}
	auditLog(sshaudit.EventCommandExec, inst.ID, getUsername(r),
		fmt.Sprintf("target=browser command=echo SSH test, latency=%dms, result=success", latency))
	writeJSON(w, http.StatusOK, sshTestResponse(target, "ok", output, latency, ""))
}

// normalizeSSHTarget canonicalises the optional `target` query parameter on
// /ssh-test and /ssh-reconnect. Unknown / empty values fall back to "agent"
// so existing clients keep working unchanged.
func normalizeSSHTarget(s string) string {
	switch s {
	case "browser":
		return "browser"
	default:
		return "agent"
	}
}

func sshTestResponse(target, status, output string, latencyMs int64, errMsg string) map[string]interface{} {
	resp := map[string]interface{}{
		"status":     status,
		"output":     output,
		"latency_ms": latencyMs,
		"target":     target,
	}
	if errMsg == "" {
		resp["error"] = nil
	} else {
		resp["error"] = errMsg
	}
	return resp
}

func sshReconnectResponse(target, status string, latencyMs int64, errMsg string) map[string]interface{} {
	resp := map[string]interface{}{
		"status":     status,
		"latency_ms": latencyMs,
		"target":     target,
	}
	if errMsg == "" {
		resp["error"] = nil
	} else {
		resp["error"] = errMsg
	}
	return resp
}

// GetSSHStatus returns the SSH connection status, health metrics, active tunnels,
// and recent state transitions for an instance.
func GetSSHStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	// Connection state
	state := SSHMgr.GetConnectionState(inst.ID)

	// Health metrics
	var metricsResp *sshStatusMetrics
	if m := SSHMgr.GetMetrics(inst.ID); m != nil {
		metricsResp = &sshStatusMetrics{
			ConnectedAt:      formatTimestamp(m.ConnectedAt),
			LastHealthCheck:  formatTimestamp(m.LastHealthCheck),
			Uptime:           m.Uptime().String(),
			SuccessfulChecks: m.SuccessfulChecks,
			FailedChecks:     m.FailedChecks,
		}
	}

	// Active tunnels with health status
	var tunnelsResp []sshStatusTunnel
	if TunnelMgr != nil {
		tunnelMetrics := TunnelMgr.GetTunnelMetrics(inst.ID)
		tunnelsResp = make([]sshStatusTunnel, len(tunnelMetrics))
		for i, tm := range tunnelMetrics {
			tunnelsResp[i] = sshStatusTunnel{
				Label:            tm.Label,
				LocalPort:        tm.LocalPort,
				RemotePort:       tm.RemotePort,
				Status:           tm.Status,
				CreatedAt:        formatTimestamp(tm.CreatedAt),
				LastHealthCheck:  formatTimestamp(tm.LastHealthCheck),
				SuccessfulChecks: tm.SuccessfulChecks,
				FailedChecks:     tm.FailedChecks,
				Uptime:           tm.Uptime.String(),
			}
		}
	}
	if tunnelsResp == nil {
		tunnelsResp = []sshStatusTunnel{}
	}

	// Recent state transitions (last 10)
	allTransitions := SSHMgr.GetStateTransitions(inst.ID)
	recentTransitions := allTransitions
	if len(allTransitions) > 10 {
		recentTransitions = allTransitions[len(allTransitions)-10:]
	}
	eventsResp := make([]sshStatusEvent, len(recentTransitions))
	for i, t := range recentTransitions {
		eventsResp[i] = sshStatusEvent{
			From:      t.From.String(),
			To:        t.To.String(),
			Timestamp: t.Timestamp.UTC().Format(time.RFC3339),
			Reason:    t.Reason,
		}
	}

	writeJSON(w, http.StatusOK, sshStatusResponse{
		State:        state.String(),
		Metrics:      metricsResp,
		Tunnels:      tunnelsResp,
		RecentEvents: eventsResp,
	})
}

type sshStatusResponse struct {
	State        string            `json:"state"`
	Metrics      *sshStatusMetrics `json:"metrics"`
	Tunnels      []sshStatusTunnel `json:"tunnels"`
	RecentEvents []sshStatusEvent  `json:"recent_events"`
}

type sshStatusMetrics struct {
	ConnectedAt      string `json:"connected_at"`
	LastHealthCheck  string `json:"last_health_check"`
	Uptime           string `json:"uptime"`
	SuccessfulChecks int64  `json:"successful_checks"`
	FailedChecks     int64  `json:"failed_checks"`
}

type sshStatusTunnel struct {
	Label            string `json:"label"`
	LocalPort        int    `json:"local_port"`
	RemotePort       int    `json:"remote_port"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
	LastHealthCheck  string `json:"last_health_check"`
	SuccessfulChecks int64  `json:"successful_checks"`
	FailedChecks     int64  `json:"failed_checks"`
	Uptime           string `json:"uptime"`
}

type sshStatusEvent struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Timestamp string `json:"timestamp"`
	Reason    string `json:"reason"`
}

// GetTunnelStatus returns the active SSH tunnels for an instance.
func GetTunnelStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if TunnelMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"tunnels": []interface{}{},
			"error":   "Tunnel manager not initialized",
		})
		return
	}

	tunnels := TunnelMgr.GetTunnelsForInstance(inst.ID)

	type tunnelResponse struct {
		Label      string `json:"label"`
		Type       string `json:"type"`
		LocalPort  int    `json:"local_port"`
		RemotePort int    `json:"remote_port"`
		Status     string `json:"status"`
		Error      string `json:"error,omitempty"`
		LastCheck  string `json:"last_check"`
	}

	resp := make([]tunnelResponse, len(tunnels))
	for i, t := range tunnels {
		resp[i] = tunnelResponse{
			Label:      t.Label,
			Type:       string(t.Config.Type),
			LocalPort:  t.LocalPort,
			RemotePort: t.Config.RemotePort,
			Status:     t.Status,
			Error:      t.Error,
			LastCheck:  t.LastCheck.UTC().Format(time.RFC3339),
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tunnels": resp,
	})
}

// GetSSHEvents returns the SSH connection event history for an instance.
// Events include connections, disconnections, health check failures,
// reconnection attempts, and public key uploads.
func GetSSHEvents(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	events := SSHMgr.GetEventHistory(inst.ID)
	resp := make([]sshEventEntry, 0, len(events))
	for _, e := range events {
		resp = append(resp, sshEventEntry{
			Type:      string(e.Type),
			Timestamp: e.Timestamp.UTC().Format(time.RFC3339),
			Details:   e.Details,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"events": resp,
	})
}

type sshEventEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Details   string `json:"details"`
}

// SSHReconnect triggers a manual reconnection to an instance. It closes the
// existing connection and re-establishes it with key re-upload.
//
// `target` query parameter (default "agent") selects the agent pod or the
// per-instance browser pod, mirroring SSHConnectionTest.
func SSHReconnect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}

	var inst database.Instance
	if err := database.DB.First(&inst, id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Instance not found")
		return
	}

	if !middleware.CanAccessInstance(r, inst.ID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	target := normalizeSSHTarget(r.URL.Query().Get("target"))

	if target == "browser" {
		if database.IsLegacyEmbedded(inst.ContainerImage) {
			writeJSON(w, http.StatusOK, sshReconnectResponse(target, "error", 0,
				"Legacy instance: browser runs inside the agent pod, no separate browser SSH endpoint"))
			return
		}
		if BrowserBridgeRef == nil {
			writeJSON(w, http.StatusServiceUnavailable, sshReconnectResponse(target, "error", 0,
				"Browser bridge not configured"))
			return
		}
		start := time.Now()
		err = BrowserBridgeRef.Reconnect(r.Context(), inst.ID)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			writeJSON(w, http.StatusOK, sshReconnectResponse(target, "error", latency, err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, sshReconnectResponse(target, "ok", latency, ""))
		return
	}

	if SSHMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, sshReconnectResponse(target, "error", 0, "SSH manager not initialized"))
		return
	}

	start := time.Now()
	err = SSHMgr.ReconnectWithBackoff(r.Context(), inst.ID, 3, "manual reconnect")
	latency := time.Since(start).Milliseconds()

	if err != nil {
		writeJSON(w, http.StatusOK, sshReconnectResponse(target, "error", latency, err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, sshReconnectResponse(target, "ok", latency, ""))
}

// GetSSHFingerprint returns the global SSH public key fingerprint.
// This is a control-plane-wide value (not per-instance).
func GetSSHFingerprint(w http.ResponseWriter, r *http.Request) {
	if SSHMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "SSH manager not initialized")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"fingerprint": SSHMgr.GetPublicKeyFingerprint(),
		"public_key":  SSHMgr.GetPublicKey(),
	})
}
