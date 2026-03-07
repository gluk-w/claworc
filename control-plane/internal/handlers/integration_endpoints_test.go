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
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// withRunningInstance creates a provider and instance, waits for the instance
// to reach "running" status, calls fn, then cleans up.
func withRunningInstance(t *testing.T, fn func(instID uint, instName string)) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}

	// Create provider
	provBody, _ := json.Marshal(map[string]interface{}{
		"key":      fmt.Sprintf("test-%d", time.Now().UnixNano()),
		"name":     "Test Provider",
		"base_url": "https://api.openai.com/v1",
		"api_type": "openai-completions",
	})
	resp, err := client.Post(sessionURL+"/api/v1/llm/providers", "application/json", bytes.NewReader(provBody))
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create provider: expected 201, got %d: %s", resp.StatusCode, body)
	}
	var provResp struct {
		ID uint `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&provResp)
	resp.Body.Close()
	provID := provResp.ID

	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/llm/providers/%d", sessionURL, provID), nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Warning: delete provider %d: %v", provID, err)
			return
		}
		resp.Body.Close()
	}()

	// Create instance
	displayName := fmt.Sprintf("eptest-%d", time.Now().UnixNano())
	instBody, _ := json.Marshal(map[string]interface{}{
		"display_name":      displayName,
		"enabled_providers": []uint{provID},
	})
	resp, err = client.Post(sessionURL+"/api/v1/instances", "application/json", bytes.NewReader(instBody))
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create instance: expected 201, got %d: %s", resp.StatusCode, body)
	}
	var instResp struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&instResp)
	resp.Body.Close()
	instID := instResp.ID
	instName := instResp.Name
	t.Logf("Created instance id=%d name=%s", instID, instName)

	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/instances/%d", sessionURL, instID), nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Warning: delete instance %d: %v", instID, err)
			return
		}
		resp.Body.Close()
		t.Logf("Deleted instance id=%d name=%s", instID, instName)
	}()

	// Poll until running
	t.Log("Waiting for instance to reach 'running'...")
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", sessionURL, instID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var poll map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&poll)
		resp.Body.Close()
		status, _ := poll["status"].(string)
		t.Logf("Instance status: %s", status)
		if status == "running" {
			break
		}
		if status == "error" {
			t.Fatalf("Instance entered error status: %v", poll["status_message"])
		}
		time.Sleep(2 * time.Second)
	}

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

		// Expect VNC and Gateway reverse tunnels to be present and active
		if len(body.Tunnels) == 0 {
			t.Error("tunnels is empty, expected at least VNC and Gateway tunnels")
		} else {
			for _, tun := range body.Tunnels {
				t.Logf("tunnel: label=%q status=%q", tun.Label, tun.Status)
			}
			for _, wantLabel := range []string{"VNC", "Gateway"} {
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
