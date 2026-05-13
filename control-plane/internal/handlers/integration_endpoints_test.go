//go:build docker_integration

package handlers_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/handlers"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// sharedEndpointInstance is created once and reused across all integration_endpoints
// tests that just need a running, SSH-connected instance with an attached provider.
// Building a Docker container takes ~60s, so a single shared instance saves
// minutes of CI time across the suite.
var (
	sharedEndpointInstanceOnce sync.Once
	sharedEndpointInstanceID   uint
	sharedEndpointInstanceName string
	sharedEndpointInstanceErr  error
)

func getSharedEndpointInstance() (uint, string, error) {
	sharedEndpointInstanceOnce.Do(func() {
		sharedEndpointInstanceID, sharedEndpointInstanceName, sharedEndpointInstanceErr = createSharedEndpointInstance()
	})
	return sharedEndpointInstanceID, sharedEndpointInstanceName, sharedEndpointInstanceErr
}

func createSharedEndpointInstance() (uint, string, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	provBody, _ := json.Marshal(map[string]interface{}{
		"key":      fmt.Sprintf("test-%d", time.Now().UnixNano()),
		"name":     "Test Provider",
		"base_url": "https://api.openai.com/v1",
		"api_type": "openai-completions",
	})
	resp, err := client.Post(sessionURL+"/api/v1/llm/providers", "application/json", bytes.NewReader(provBody))
	if err != nil {
		return 0, "", fmt.Errorf("create provider: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return 0, "", fmt.Errorf("create provider: expected 201, got %d: %s", resp.StatusCode, body)
	}
	var provResp struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&provResp)
	resp.Body.Close()
	provID := provResp.ID

	displayName := fmt.Sprintf("eptest-%d", time.Now().UnixNano())
	instBody, _ := json.Marshal(map[string]interface{}{
		"display_name":      displayName,
		"team_id":           1,
		"enabled_providers": []uint{provID},
	})
	resp, err = client.Post(sessionURL+"/api/v1/instances", "application/json", bytes.NewReader(instBody))
	if err != nil {
		return 0, "", fmt.Errorf("create instance: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return 0, "", fmt.Errorf("create instance: expected 201, got %d: %s", resp.StatusCode, body)
	}
	var instResp struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&instResp)
	resp.Body.Close()

	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		r, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", sessionURL, instResp.ID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var poll map[string]interface{}
		json.NewDecoder(r.Body).Decode(&poll)
		r.Body.Close()
		status, _ := poll["status"].(string)
		if status == "running" {
			return instResp.ID, instResp.Name, nil
		}
		if status == "error" {
			return 0, "", fmt.Errorf("instance entered error status: %v", poll["status_message"])
		}
		time.Sleep(2 * time.Second)
	}
	return 0, "", fmt.Errorf("instance did not reach 'running' within 120s")
}

// withRunningInstance returns the shared running instance, creating it on first use.
// All endpoint tests reuse the same container; do not perform destructive operations
// (deletion, restart) on it.
func withRunningInstance(t *testing.T, fn func(instID uint, instName string)) {
	t.Helper()
	instID, instName, err := getSharedEndpointInstance()
	if err != nil {
		t.Fatalf("shared endpoint instance: %v", err)
	}
	t.Logf("Using shared endpoint instance id=%d name=%s", instID, instName)
	fn(instID, instName)
}

// waitForSSHConnected polls ssh-status until state == "connected" or timeout.
func waitForSSHConnected(t *testing.T, instID uint, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d/ssh-status", sessionURL, instID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var status struct {
			State string `json:"state"`
		}
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		t.Logf("SSH state: %s", status.State)
		if status.State == "connected" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("SSH did not reach 'connected' state within %s", timeout)
}

// ─── SSH Status ───────────────────────────────────────────────────────────────

func TestIntegration_SSHStatus(t *testing.T) {
	t.Parallel()
	withRunningInstance(t, func(instID uint, _ string) {
		waitForSSHConnected(t, instID, 90*time.Second)

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d/ssh-status", sessionURL, instID))
		if err != nil {
			t.Fatalf("GET ssh-status: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("GET ssh-status: expected 200, got %d: %s", resp.StatusCode, body)
		}

		var body struct {
			State   string `json:"state"`
			Metrics *struct {
				ConnectedAt      string `json:"connected_at"`
				SuccessfulChecks int64  `json:"successful_checks"`
				FailedChecks     int64  `json:"failed_checks"`
				Uptime           string `json:"uptime"`
			} `json:"metrics"`
			Tunnels []struct {
				Label  string `json:"label"`
				Status string `json:"status"`
			} `json:"tunnels"`
			RecentEvents []struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"recent_events"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode ssh-status response: %v", err)
		}
		resp.Body.Close()

		// state
		if body.State != "connected" {
			t.Errorf("state = %q, want %q", body.State, "connected")
		} else {
			t.Logf("state = %q ✓", body.State)
		}

		// metrics present
		if body.Metrics == nil {
			t.Error("metrics is nil, want non-nil for a connected instance")
		} else {
			if body.Metrics.ConnectedAt == "" {
				t.Error("metrics.connected_at is empty")
			}
			t.Logf("metrics.connected_at = %q, uptime = %q ✓", body.Metrics.ConnectedAt, body.Metrics.Uptime)
		}

		// Expect CDP and Gateway reverse tunnels to be present and active
		if len(body.Tunnels) == 0 {
			t.Error("tunnels is empty, expected at least CDP and Gateway tunnels")
		} else {
			for _, tun := range body.Tunnels {
				t.Logf("tunnel: label=%q status=%q", tun.Label, tun.Status)
			}
			for _, wantLabel := range []string{"CDP", "Gateway"} {
				found := false
				for _, tun := range body.Tunnels {
					if tun.Label == wantLabel {
						found = true
						if tun.Status != "active" {
							t.Errorf("%s tunnel status = %q, want %q", wantLabel, tun.Status, "active")
						} else {
							t.Logf("%s tunnel status = %q ✓", wantLabel, tun.Status)
						}
					}
				}
				if !found {
					t.Errorf("%s tunnel not found in tunnels list", wantLabel)
				}
			}
		}

		// state transitions recorded
		if len(body.RecentEvents) == 0 {
			t.Error("recent_events is empty, expected at least one state transition")
		} else {
			t.Logf("recent_events: %d transitions (last: %q→%q) ✓",
				len(body.RecentEvents),
				body.RecentEvents[len(body.RecentEvents)-1].From,
				body.RecentEvents[len(body.RecentEvents)-1].To)
		}
	})
}

// ─── Terminal WebSocket ───────────────────────────────────────────────────────

func TestIntegration_Terminal(t *testing.T) {
	t.Parallel()
	withRunningInstance(t, func(instID uint, _ string) {
		waitForSSHConnected(t, instID, 90*time.Second)

		wsBase := strings.Replace(sessionURL, "http://", "ws://", 1)
		termURL := fmt.Sprintf("%s/api/v1/instances/%d/terminal", wsBase, instID)

		// ── Connect ──────────────────────────────────────────────────────────
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, termURL, nil)
		if err != nil {
			t.Fatalf("WebSocket dial: %v", err)
		}
		defer conn.CloseNow()

		// First message must be text: {"type":"session_info","session_id":"..."}
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read session_info: %v", err)
		}
		if msgType != websocket.MessageText {
			t.Fatalf("first message type = %v, want Text", msgType)
		}
		var info struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(data, &info); err != nil {
			t.Fatalf("parse session_info: %v (raw: %s)", err, data)
		}
		if info.Type != "session_info" {
			t.Errorf("session_info.type = %q, want %q", info.Type, "session_info")
		}
		if info.SessionID == "" {
			t.Fatal("session_info.session_id is empty")
		}
		sessionID := info.SessionID
		t.Logf("session_info received: session_id=%s ✓", sessionID)

		// ── Send command and collect output ───────────────────────────────────
		marker := "claworc_integration_test_marker"
		if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo "+marker+"\n")); err != nil {
			t.Fatalf("write command: %v", err)
		}

		var outputBuf strings.Builder
		readCtx, readCancel := context.WithTimeout(ctx, 15*time.Second)
		defer readCancel()
		for {
			_, chunk, err := conn.Read(readCtx)
			if err != nil {
				break
			}
			outputBuf.Write(chunk)
			if strings.Contains(outputBuf.String(), marker) {
				break
			}
		}
		output := outputBuf.String()
		if !strings.Contains(output, marker) {
			t.Errorf("terminal output did not contain marker %q within 15s; got: %q", marker, output)
		} else {
			t.Logf("marker found in terminal output ✓")
		}

		// ── Verify session appears in sessions list ───────────────────────────
		httpClient := &http.Client{Timeout: 10 * time.Second}
		listResp, err := httpClient.Get(fmt.Sprintf("%s/api/v1/instances/%d/terminal/sessions", sessionURL, instID))
		if err != nil {
			t.Fatalf("GET terminal/sessions: %v", err)
		}
		if listResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(listResp.Body)
			listResp.Body.Close()
			t.Fatalf("GET terminal/sessions: expected 200, got %d: %s", listResp.StatusCode, body)
		}
		var sessionsBody struct {
			Sessions []struct {
				ID       string `json:"id"`
				Attached bool   `json:"attached"`
			} `json:"sessions"`
		}
		json.NewDecoder(listResp.Body).Decode(&sessionsBody)
		listResp.Body.Close()

		if len(sessionsBody.Sessions) == 0 {
			t.Error("sessions list is empty, expected at least one")
		} else {
			found := false
			for _, s := range sessionsBody.Sessions {
				if s.ID == sessionID {
					found = true
					if !s.Attached {
						t.Logf("Warning: session %s shows attached=false before disconnect", sessionID)
					}
				}
			}
			if !found {
				t.Errorf("session %s not found in sessions list", sessionID)
			} else {
				t.Logf("session %s found in sessions list ✓", sessionID)
			}
		}

		// ── Disconnect and reconnect ──────────────────────────────────────────
		conn.Close(websocket.StatusNormalClosure, "")
		time.Sleep(200 * time.Millisecond) // let the server process the detach

		reconnCtx, reconnCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer reconnCancel()

		conn2, _, err := websocket.Dial(reconnCtx, termURL+"?session_id="+sessionID, nil)
		if err != nil {
			t.Fatalf("WebSocket reconnect dial: %v", err)
		}
		defer conn2.CloseNow()

		// First message on reconnect: session_info with the same session_id
		msgType2, data2, err := conn2.Read(reconnCtx)
		if err != nil {
			t.Fatalf("read session_info on reconnect: %v", err)
		}
		if msgType2 != websocket.MessageText {
			t.Fatalf("reconnect first message type = %v, want Text", msgType2)
		}
		var info2 struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(data2, &info2); err != nil {
			t.Fatalf("parse reconnect session_info: %v", err)
		}
		if info2.SessionID != sessionID {
			t.Errorf("reconnect session_id = %q, want %q", info2.SessionID, sessionID)
		} else {
			t.Logf("reconnect session_id matches ✓")
		}

		conn2.Close(websocket.StatusNormalClosure, "")
	})
}

// ─── Logs Streaming ───────────────────────────────────────────────────────────

func TestIntegration_LogsStreaming(t *testing.T) {
	t.Parallel()
	withRunningInstance(t, func(instID uint, _ string) {
		waitForSSHConnected(t, instID, 90*time.Second)

		// Use follow=false so the stream closes after reading the tail.
		// Use type=sshd: SSH connections happen during instance setup, so the
		// log will have content even if OpenClaw hasn't produced output yet.
		logsURL := fmt.Sprintf("%s/api/v1/instances/%d/logs?type=sshd&tail=20&follow=false", sessionURL, instID)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, logsURL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET logs: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET logs: expected 200, got %d: %s", resp.StatusCode, body)
		}

		// Verify SSE content-type header
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/event-stream") {
			t.Errorf("Content-Type = %q, want text/event-stream", ct)
		} else {
			t.Logf("Content-Type = %q ✓", ct)
		}

		// Read the full SSE body (closes when tail exits with follow=false)
		scanner := bufio.NewScanner(resp.Body)
		var dataLines []string
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue // blank separator between SSE events
			}
			if !strings.HasPrefix(line, "data: ") {
				t.Errorf("unexpected SSE line format: %q (want \"data: ...\")", line)
			} else {
				payload := strings.TrimPrefix(line, "data: ")
				dataLines = append(dataLines, payload)
			}
		}
		if err := scanner.Err(); err != nil && err != io.EOF && ctx.Err() == nil {
			t.Logf("scanner ended with: %v (may be normal on stream close)", err)
		}

		t.Logf("received %d SSE data lines from sshd log ✓", len(dataLines))

		// The sshd log must have at least one entry because the SSH key-upload
		// and health-check connections happen before the instance reaches "running".
		if len(dataLines) == 0 {
			t.Error("expected at least one SSE data line from sshd log, got none")
		}
	})
}

// ─── LLM Gateway ──────────────────────────────────────────────────────────────

func TestIntegration_LLMGateway(t *testing.T) {
	// Port 40001 is required for the LLMProxy agent-listener tunnel (PermitListen restriction).
	if sessionGatewayPort != 40001 {
		t.Skipf("gateway port is %d, not 40001; LLMProxy tunnel disabled (port in use at startup)", sessionGatewayPort)
	}
	t.Parallel()

	withRunningInstance(t, func(instID uint, instName string) {
		waitForSSHConnected(t, instID, 90*time.Second)

		// Poll openclaw.json until models.providers is configured.
		// ConfigureInstance writes virtual keys there after SSH connection is established.
		type providerEntry struct {
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey"`
		}
		type openClawCfg struct {
			Models struct {
				Providers map[string]providerEntry `json:"providers"`
			} `json:"models"`
		}

		var gatewayURL, virtualKey string
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			out, err := exec.Command("docker", "exec", instName, "cat", orchestrator.PathOpenClawConfig).Output()
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}
			var cfg openClawCfg
			if err := json.Unmarshal(out, &cfg); err != nil {
				time.Sleep(3 * time.Second)
				continue
			}
			for _, prov := range cfg.Models.Providers {
				if prov.APIKey != "" {
					gatewayURL = prov.BaseURL
					virtualKey = prov.APIKey
					break
				}
			}
			if virtualKey != "" {
				break
			}
			time.Sleep(3 * time.Second)
		}
		if virtualKey == "" {
			t.Fatal("openclaw.json models.providers not configured within 90s")
		}
		t.Logf("gateway baseUrl=%s, virtual key prefix=%s...", gatewayURL, virtualKey[:min(16, len(virtualKey))])

		// Wait for LLMProxy tunnel to appear as active in ssh-status.
		tunnelURL := fmt.Sprintf("%s/api/v1/instances/%d/ssh-status", sessionURL, instID)
		httpClient := &http.Client{Timeout: 10 * time.Second}
		deadline = time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := httpClient.Get(tunnelURL)
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			var status struct {
				Tunnels []struct {
					Label  string `json:"label"`
					Status string `json:"status"`
				} `json:"tunnels"`
			}
			json.NewDecoder(resp.Body).Decode(&status)
			resp.Body.Close()
			for _, tun := range status.Tunnels {
				if tun.Label == "LLMProxy" && tun.Status == "active" {
					t.Logf("LLMProxy tunnel is active ✓")
					goto tunnelReady
				}
			}
			t.Log("LLMProxy tunnel not yet active, retrying...")
			time.Sleep(2 * time.Second)
		}
		t.Fatal("LLMProxy tunnel did not become active within 60s")
	tunnelReady:

		// Get SSH client.
		sshCtx, sshCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer sshCancel()
		client, err := handlers.SSHMgr.WaitForSSH(sshCtx, instID, 10*time.Second)
		if err != nil {
			t.Fatalf("get SSH client: %v", err)
		}

		curlBase := fmt.Sprintf("%s/v1/models", gatewayURL)

		// ── Test 1: invalid key → 401 ────────────────────────────────────────
		sess1, err := client.NewSession()
		if err != nil {
			t.Fatalf("open SSH session: %v", err)
		}
		out1, _ := sess1.CombinedOutput(fmt.Sprintf(
			"curl -s -o /dev/null -w '%%{http_code}' -H 'Authorization: Bearer invalid-key' '%s'",
			curlBase))
		sess1.Close()

		code1 := strings.TrimSpace(string(out1))
		if code1 != "401" {
			t.Errorf("invalid key: want HTTP 401, got %s", code1)
		} else {
			t.Logf("invalid key → HTTP 401 ✓")
		}

		// ── Test 2: valid virtual key → gateway accepts (upstream may still fail) ──
		// The gateway must accept our virtual key. If the upstream returns 401 because
		// no real API key is stored, the gateway proxies that response faithfully.
		// We distinguish gateway auth failure (body has "authentication_error") from
		// an upstream failure (any other body).
		sess2, err := client.NewSession()
		if err != nil {
			t.Fatalf("open SSH session: %v", err)
		}
		out2, _ := sess2.CombinedOutput(fmt.Sprintf(
			`curl -s -w '\n%%{http_code}' -H 'Authorization: Bearer %s' '%s'`,
			virtualKey, curlBase))
		sess2.Close()

		lines := strings.Split(strings.TrimSpace(string(out2)), "\n")
		code2 := lines[len(lines)-1]
		body2 := strings.Join(lines[:len(lines)-1], "\n")

		if code2 == "401" && strings.Contains(body2, `"authentication_error"`) {
			t.Errorf("valid key: gateway rejected our virtual key with auth error (body: %s)", body2)
		} else {
			t.Logf("valid key → HTTP %s ✓ (gateway accepted key; upstream response body: %.120s)", code2, body2)
		}
	})
}

// ─── Gateway basePath ────────��───────────────────────────────────────────────

func TestIntegration_GatewayBasePath(t *testing.T) {
	t.Skip("Requires updated agent image with CLAWORC_INSTANCE_ID basePath support; enable after agent image rebuild")
	withRunningInstance(t, func(instID uint, instName string) {
		waitForSSHConnected(t, instID, 90*time.Second)

		// Step 1: Verify openclaw.json has gateway.controlUi.basePath set.
		// The s6 startup script sets this from the CLAWORC_INSTANCE_ID env var.
		type openClawCfg struct {
			Gateway struct {
				ControlUI struct {
					BasePath string `json:"basePath"`
				} `json:"controlUi"`
			} `json:"gateway"`
		}

		expectedBasePath := fmt.Sprintf("/openclaw/%d/", instID)
		deadline := time.Now().Add(90 * time.Second)
		configured := false
		for time.Now().Before(deadline) {
			out, err := exec.Command("docker", "exec", instName, "cat", orchestrator.PathOpenClawConfig).Output()
			if err != nil {
				time.Sleep(3 * time.Second)
				continue
			}
			var cfg openClawCfg
			if err := json.Unmarshal(out, &cfg); err != nil {
				time.Sleep(3 * time.Second)
				continue
			}
			if cfg.Gateway.ControlUI.BasePath == expectedBasePath {
				t.Logf("gateway.controlUi.basePath = %q ✓", cfg.Gateway.ControlUI.BasePath)
				configured = true
				break
			}
			t.Logf("gateway.controlUi.basePath = %q, want %q — retrying", cfg.Gateway.ControlUI.BasePath, expectedBasePath)
			time.Sleep(3 * time.Second)
		}
		if !configured {
			t.Fatalf("gateway.controlUi.basePath was not set to %q within 90s", expectedBasePath)
		}

		// Step 2: Hit Claworc's /openclaw/{id}/ endpoint (the full proxy chain:
		// HTTP client → Claworc ControlProxy → SSH tunnel → OpenClaw gateway)
		// and verify the gateway returns the Control UI HTML.
		client := &http.Client{Timeout: 30 * time.Second}
		controlURL := fmt.Sprintf("%s/openclaw/%d/", sessionURL, instID)

		var resp *http.Response
		var respErr error
		deadline = time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			resp, respErr = client.Get(controlURL)
			if respErr == nil && resp.StatusCode == http.StatusOK {
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			t.Log("Control UI not yet available via proxy — retrying")
			time.Sleep(3 * time.Second)
		}
		if respErr != nil {
			t.Fatalf("GET %s: %v", controlURL, respErr)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d, want 200", controlURL, resp.StatusCode)
		}
		t.Logf("GET %s → HTTP %d ✓", controlURL, resp.StatusCode)

		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		} else {
			t.Logf("Content-Type = %q ✓", ct)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read response body: %v", err)
		}
		html := string(body)
		if !strings.Contains(html, "<html") {
			t.Errorf("response body does not contain <html; got %.200s", html)
		} else {
			t.Logf("response body contains Control UI HTML ✓")
		}

		// Verify asset paths use relative "./" prefix so the browser resolves
		// them under the proxy path (e.g. /openclaw/1/assets/...).
		if !strings.Contains(html, `src="./assets/`) {
			t.Errorf("HTML does not contain relative script src=\"./assets/\" paths")
		} else {
			t.Logf("script src uses relative ./assets/ paths ✓")
		}
		// Absolute /assets/ paths would break under the proxy — flag them.
		if strings.Contains(html, `src="/assets/`) {
			t.Errorf("HTML contains absolute src=\"/assets/\" paths — would break under proxy")
		}
	})
}

// ─── Control Proxy: root-resource fallback ────────────────────────────────────

// TestIntegration_ControlProxy_FaviconFallback verifies that when a request is
// proxied through /openclaw/{id}/{resource} and the gateway returns 404, the
// proxy retries with just /{resource} against the instance root. Browsers
// automatically request /favicon.svg from the document root regardless of
// any injected <base href>, so this fallback is required to serve favicons
// (and similar root-level resources) through the control proxy.
func TestIntegration_ControlProxy_FaviconFallback(t *testing.T) {
	t.Skip("Skipped in CI: gateway tunnel takes >90s to initialise in GitHub Actions Docker environment")
	withRunningInstance(t, func(instID uint, _ string) {
		waitForSSHConnected(t, instID, 90*time.Second)

		client := &http.Client{Timeout: 30 * time.Second}

		// Wait for the Gateway tunnel to come up. Until it does, the proxy
		// returns the "Connecting to OpenClaw…" 503 placeholder regardless of
		// path.
		gatewayReady := false
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d/ssh-status", sessionURL, instID))
			if err == nil {
				var status struct {
					Tunnels []struct {
						Label  string `json:"label"`
						Status string `json:"status"`
					} `json:"tunnels"`
				}
				json.NewDecoder(resp.Body).Decode(&status)
				resp.Body.Close()
				for _, tun := range status.Tunnels {
					if tun.Label == "Gateway" && tun.Status == "active" {
						gatewayReady = true
						break
					}
				}
			}
			if gatewayReady {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !gatewayReady {
			t.Fatal("Gateway tunnel did not become active within 90s")
		}

		// Request favicon.svg via the proxy. This is the exact case that
		// motivated the fallback: the browser asks for /openclaw/{id}/favicon.svg
		// but the gateway only serves /favicon.svg (browsers ignore <base> for
		// the implicit favicon request).
		//
		// Poll for up to 60s in case the gateway process is still initializing
		// even after the SSH tunnel is active.
		faviconURL := fmt.Sprintf("%s/openclaw/%d/favicon.svg", sessionURL, instID)
		var lastStatus int
		var lastBody []byte
		deadline = time.Now().Add(60 * time.Second)
		got200 := false
		for time.Now().Before(deadline) {
			resp, err := client.Get(faviconURL)
			if err != nil {
				t.Logf("GET %s: %v — retrying", faviconURL, err)
				time.Sleep(2 * time.Second)
				continue
			}
			lastStatus = resp.StatusCode
			lastBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				got200 = true
				break
			}
			t.Logf("GET %s: HTTP %d — retrying", faviconURL, resp.StatusCode)
			time.Sleep(2 * time.Second)
		}
		if !got200 {
			t.Fatalf("GET %s: last status %d (body: %.200s)", faviconURL, lastStatus, lastBody)
		}
		if len(lastBody) == 0 {
			t.Error("favicon.svg response body is empty")
		} else {
			t.Logf("GET %s → HTTP 200, %d bytes ✓", faviconURL, len(lastBody))
		}

		// Negative case: a path that exists neither under the prefix nor at
		// the root must still return 404. This verifies the fallback does not
		// mask legitimate 404s — it only kicks in for paths that happen to
		// live at the instance root.
		missingURL := fmt.Sprintf("%s/openclaw/%d/definitely-not-a-real-path-xyz-12345.bin", sessionURL, instID)
		resp, err := client.Get(missingURL)
		if err != nil {
			t.Fatalf("GET %s: %v", missingURL, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: expected 404, got %d (body: %.200s)", missingURL, resp.StatusCode, body)
		} else {
			t.Logf("GET %s → HTTP 404 ✓ (fallback did not mask a real 404)", missingURL)
		}
	})
}
