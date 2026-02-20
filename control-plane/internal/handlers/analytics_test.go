package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupAnalyticsDB creates a fresh in-memory SQLite database with telemetry table for analytics tests.
func setupAnalyticsDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	database.DB = db
	if err := db.AutoMigrate(&database.Setting{}, &database.ProviderTelemetry{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func TestGetProviderAnalytics_Empty(t *testing.T) {
	setupAnalyticsDB(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/providers", nil)
	rec := httptest.NewRecorder()
	GetProviderAnalytics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	providers, ok := result["providers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected providers to be a map")
	}
	if len(providers) != 0 {
		t.Fatalf("expected empty providers, got %d", len(providers))
	}

	periodDays, ok := result["period_days"].(float64)
	if !ok || periodDays != 7 {
		t.Fatalf("expected period_days=7, got %v", result["period_days"])
	}
}

func TestGetProviderAnalytics_WithData(t *testing.T) {
	setupAnalyticsDB(t)

	// Insert test telemetry data
	now := time.Now()
	entries := []database.ProviderTelemetry{
		{Provider: "openai", StatusCode: 200, Latency: 100, IsError: false, CreatedAt: now},
		{Provider: "openai", StatusCode: 200, Latency: 200, IsError: false, CreatedAt: now},
		{Provider: "openai", StatusCode: 401, Latency: 50, IsError: true, ErrorMsg: "Invalid API key", CreatedAt: now},
		{Provider: "anthropic", StatusCode: 200, Latency: 150, IsError: false, CreatedAt: now},
	}
	for _, e := range entries {
		if err := database.RecordTelemetry(&e); err != nil {
			t.Fatalf("record telemetry: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/providers", nil)
	rec := httptest.NewRecorder()
	GetProviderAnalytics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var providers map[string]database.ProviderStats
	if err := json.Unmarshal(result["providers"], &providers); err != nil {
		t.Fatalf("unmarshal providers: %v", err)
	}

	// Check openai stats
	openai, ok := providers["openai"]
	if !ok {
		t.Fatal("expected openai stats")
	}
	if openai.TotalRequests != 3 {
		t.Fatalf("expected 3 total requests for openai, got %d", openai.TotalRequests)
	}
	if openai.ErrorCount != 1 {
		t.Fatalf("expected 1 error for openai, got %d", openai.ErrorCount)
	}
	// Error rate should be ~0.333
	if openai.ErrorRate < 0.3 || openai.ErrorRate > 0.4 {
		t.Fatalf("expected error rate ~0.33, got %f", openai.ErrorRate)
	}
	// Average latency should be ~116.67
	if openai.AvgLatency < 100 || openai.AvgLatency > 130 {
		t.Fatalf("expected avg latency ~117, got %f", openai.AvgLatency)
	}
	if openai.LastError != "Invalid API key" {
		t.Fatalf("expected last error 'Invalid API key', got '%s'", openai.LastError)
	}

	// Check anthropic stats
	anthropic, ok := providers["anthropic"]
	if !ok {
		t.Fatal("expected anthropic stats")
	}
	if anthropic.TotalRequests != 1 {
		t.Fatalf("expected 1 total request for anthropic, got %d", anthropic.TotalRequests)
	}
	if anthropic.ErrorCount != 0 {
		t.Fatalf("expected 0 errors for anthropic, got %d", anthropic.ErrorCount)
	}
}

func TestGetProviderAnalytics_ExcludesOldData(t *testing.T) {
	setupAnalyticsDB(t)

	// Insert old telemetry data (8 days ago)
	oldTime := time.Now().AddDate(0, 0, -8)
	if err := database.RecordTelemetry(&database.ProviderTelemetry{
		Provider: "openai", StatusCode: 200, Latency: 100, IsError: false, CreatedAt: oldTime,
	}); err != nil {
		t.Fatalf("record telemetry: %v", err)
	}

	// Insert recent data
	if err := database.RecordTelemetry(&database.ProviderTelemetry{
		Provider: "anthropic", StatusCode: 200, Latency: 150, IsError: false, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("record telemetry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/providers", nil)
	rec := httptest.NewRecorder()
	GetProviderAnalytics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var providers map[string]database.ProviderStats
	if err := json.Unmarshal(result["providers"], &providers); err != nil {
		t.Fatalf("unmarshal providers: %v", err)
	}

	// Old openai data should be excluded
	if _, ok := providers["openai"]; ok {
		t.Fatal("expected old openai data to be excluded from 7-day window")
	}

	// Recent anthropic data should be included
	if _, ok := providers["anthropic"]; !ok {
		t.Fatal("expected recent anthropic data to be included")
	}
}

func TestCleanupOldTelemetry(t *testing.T) {
	setupAnalyticsDB(t)

	// Insert some telemetry
	old := time.Now().AddDate(0, 0, -10)
	recent := time.Now()
	database.RecordTelemetry(&database.ProviderTelemetry{Provider: "a", StatusCode: 200, Latency: 10, CreatedAt: old})
	database.RecordTelemetry(&database.ProviderTelemetry{Provider: "b", StatusCode: 200, Latency: 10, CreatedAt: recent})

	// Cleanup entries older than 7 days
	cutoff := time.Now().AddDate(0, 0, -7)
	if err := database.CleanupOldTelemetry(cutoff); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// Verify only recent entry remains
	var count int64
	database.DB.Model(&database.ProviderTelemetry{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 entry after cleanup, got %d", count)
	}
}
