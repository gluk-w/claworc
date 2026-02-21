package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshmanager"
	"github.com/gluk-w/claworc/control-plane/internal/sshtunnel"
)

func TestGetSSHMetrics_NoInstances(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshMetricsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.UptimeBuckets) != 5 {
		t.Errorf("expected 5 uptime buckets, got %d", len(resp.UptimeBuckets))
	}
	if len(resp.HealthRates) != 0 {
		t.Errorf("expected 0 health rates, got %d", len(resp.HealthRates))
	}
	if len(resp.ReconnectionCounts) != 0 {
		t.Errorf("expected 0 reconnection counts, got %d", len(resp.ReconnectionCounts))
	}
}

func TestGetSSHMetrics_WithConnectedInstances(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst1 := database.Instance{Name: "bot-alpha", DisplayName: "Alpha", Status: "running"}
	inst2 := database.Instance{Name: "bot-beta", DisplayName: "Beta", Status: "running"}
	database.DB.Create(&inst1)
	database.DB.Create(&inst2)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	// Set up connections with metrics via SetClient
	sm.SetClient("bot-alpha", nil)
	sm.SetClient("bot-beta", nil)
	sm.SetConnectionState("bot-alpha", sshmanager.StateConnected)
	sm.SetConnectionState("bot-beta", sshmanager.StateConnected)

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}

	var resp sshMetricsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// Both instances should be in the "< 1h" bucket (just connected)
	totalUptimeCount := 0
	for _, b := range resp.UptimeBuckets {
		totalUptimeCount += b.Count
	}
	if totalUptimeCount != 2 {
		t.Errorf("expected 2 instances in uptime buckets, got %d", totalUptimeCount)
	}
}

func TestGetSSHMetrics_HealthRates(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-healthy", DisplayName: "Healthy", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.SetClient("bot-healthy", nil)
	sm.SetConnectionState("bot-healthy", sshmanager.StateConnected)

	// Simulate health checks by directly recording them
	// Record 9 successful and 1 failed check
	for i := 0; i < 9; i++ {
		sm.RecordHealthCheckForTest("bot-healthy", nil)
	}
	sm.RecordHealthCheckForTest("bot-healthy", fmt.Errorf("test error"))

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshMetricsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.HealthRates) != 1 {
		t.Fatalf("expected 1 health rate entry, got %d", len(resp.HealthRates))
	}

	hr := resp.HealthRates[0]
	if hr.DisplayName != "Healthy" {
		t.Errorf("expected display name 'Healthy', got %q", hr.DisplayName)
	}
	if hr.TotalChecks != 10 {
		t.Errorf("expected 10 total checks, got %d", hr.TotalChecks)
	}
	// 9/10 = 0.9
	if hr.SuccessRate < 0.89 || hr.SuccessRate > 0.91 {
		t.Errorf("expected success rate ~0.9, got %f", hr.SuccessRate)
	}
}

func TestGetSSHMetrics_ReconnectionCounts(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-unstable", DisplayName: "Unstable", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.SetClient("bot-unstable", nil)
	sm.SetConnectionState("bot-unstable", sshmanager.StateConnected)

	// Simulate reconnection events
	sm.LogEvent("bot-unstable", sshmanager.EventReconnecting, "attempt 1")
	sm.LogEvent("bot-unstable", sshmanager.EventReconnecting, "attempt 2")
	sm.LogEvent("bot-unstable", sshmanager.EventReconnecting, "attempt 3")

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshMetricsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.ReconnectionCounts) != 1 {
		t.Fatalf("expected 1 reconnection count entry, got %d", len(resp.ReconnectionCounts))
	}

	rc := resp.ReconnectionCounts[0]
	if rc.DisplayName != "Unstable" {
		t.Errorf("expected display name 'Unstable', got %q", rc.DisplayName)
	}
	if rc.Count != 3 {
		t.Errorf("expected 3 reconnections, got %d", rc.Count)
	}
}

func TestGetSSHMetrics_ViewerFiltered(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst1 := database.Instance{Name: "bot-one", DisplayName: "One", Status: "running"}
	inst2 := database.Instance{Name: "bot-two", DisplayName: "Two", Status: "running"}
	database.DB.Create(&inst1)
	database.DB.Create(&inst2)

	viewer := &database.User{Username: "viewer", PasswordHash: "x", Role: "viewer"}
	database.DB.Create(viewer)

	// Only assign inst1 to viewer
	database.DB.Create(&database.UserInstance{UserID: viewer.ID, InstanceID: inst1.ID})

	sm := sshmanager.NewSSHManager(0)
	tm := sshtunnel.NewTunnelManager(sm)
	sshtunnel.SetGlobalForTest(sm, tm)
	defer sshtunnel.ResetGlobalForTest()

	sm.SetClient("bot-one", nil)
	sm.SetClient("bot-two", nil)
	sm.SetConnectionState("bot-one", sshmanager.StateConnected)
	sm.SetConnectionState("bot-two", sshmanager.StateConnected)

	// Add reconnection events for both
	sm.LogEvent("bot-one", sshmanager.EventReconnecting, "attempt 1")
	sm.LogEvent("bot-two", sshmanager.EventReconnecting, "attempt 1")
	sm.LogEvent("bot-two", sshmanager.EventReconnecting, "attempt 2")

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, viewer)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshMetricsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// Viewer should only see bot-one's metrics
	totalUptime := 0
	for _, b := range resp.UptimeBuckets {
		totalUptime += b.Count
	}
	if totalUptime != 1 {
		t.Errorf("viewer should see 1 instance in uptime, got %d", totalUptime)
	}

	// Should only have bot-one's reconnection count
	if len(resp.ReconnectionCounts) != 1 {
		t.Fatalf("viewer should see 1 reconnection entry, got %d", len(resp.ReconnectionCounts))
	}
	if resp.ReconnectionCounts[0].Count != 1 {
		t.Errorf("expected 1 reconnection for bot-one, got %d", resp.ReconnectionCounts[0].Count)
	}
}

func TestGetSSHMetrics_ResponseFormat(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	expectedFields := []string{"uptime_buckets", "health_rates", "reconnection_counts"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("response missing %q field", field)
		}
	}
}

func TestGetSSHMetrics_UptimeBucketLabels(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	var resp sshMetricsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	expectedLabels := []string{"< 1h", "1–6h", "6–24h", "1–7d", "> 7d"}
	if len(resp.UptimeBuckets) != len(expectedLabels) {
		t.Fatalf("expected %d buckets, got %d", len(expectedLabels), len(resp.UptimeBuckets))
	}
	for i, expected := range expectedLabels {
		if resp.UptimeBuckets[i].Label != expected {
			t.Errorf("bucket %d: expected label %q, got %q", i, expected, resp.UptimeBuckets[i].Label)
		}
	}
}

func TestGetSSHMetrics_NoManagers(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	inst := database.Instance{Name: "bot-test", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	admin := &database.User{Username: "admin", PasswordHash: "x", Role: "admin"}
	database.DB.Create(admin)

	sshtunnel.ResetGlobalForTest()

	r := newChiRequest("GET", "/api/v1/ssh-metrics", nil)
	r = middleware.WithUserForTest(r, admin)

	w := httptest.NewRecorder()
	GetSSHMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp sshMetricsResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// With no SSH manager, no metrics should be populated
	for _, b := range resp.UptimeBuckets {
		if b.Count != 0 {
			t.Errorf("expected 0 count for bucket %q with no manager, got %d", b.Label, b.Count)
		}
	}
	if len(resp.HealthRates) != 0 {
		t.Errorf("expected 0 health rates with no manager, got %d", len(resp.HealthRates))
	}
}
