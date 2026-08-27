package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/internalproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// fakeComposio points ComposioBaseURL at a server that records the api key it
// was called with and answers everything 200. The returned func reads the key
// under the same lock the (concurrent) probe requests write it with.
func fakeComposio(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var seenKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenKey = r.Header.Get("x-api-key")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	old := internalproxy.ComposioBaseURL
	internalproxy.ComposioBaseURL = srv.URL
	t.Cleanup(func() { internalproxy.ComposioBaseURL = old })

	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return seenKey
	}
}

func postComposioTest(t *testing.T, body string) internalproxy.PermissionReport {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/composio/test", strings.NewReader(body))
	rec := httptest.NewRecorder()
	TestComposioKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var report internalproxy.PermissionReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (body %s)", err, rec.Body.String())
	}
	return report
}

func TestTestComposioKey_NoKeyConfigured(t *testing.T) {
	setupTestDB(t)
	fakeComposio(t)

	report := postComposioTest(t, `{}`)
	if report.OK {
		t.Error("expected ok=false with no key configured")
	}
	if report.Error != "Composio API key is not configured" {
		t.Errorf("unexpected error: %q", report.Error)
	}
	if len(report.Checks) != 0 {
		t.Errorf("expected no checks, got %d", len(report.Checks))
	}
}

func TestTestComposioKey_UsesInlineKey(t *testing.T) {
	setupTestDB(t)
	seenKey := fakeComposio(t)

	report := postComposioTest(t, `{"api_key":"inline-key"}`)
	if !report.OK {
		t.Errorf("expected ok, got %+v", report)
	}
	if seenKey() != "inline-key" {
		t.Errorf("expected the inline key to be used, got %q", seenKey())
	}
}

func TestTestComposioKey_MaskedValueFallsBackToStoredKey(t *testing.T) {
	setupTestDB(t)
	seenKey := fakeComposio(t)

	enc, err := utils.Encrypt("stored-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := database.SetSetting("composio_api_key", enc); err != nil {
		t.Fatalf("set setting: %v", err)
	}

	// The UI shows "****" + last 4 chars; echoing that back must not be treated
	// as a key to test.
	report := postComposioTest(t, `{"api_key":"****-key"}`)
	if !report.OK {
		t.Errorf("expected ok, got %+v", report)
	}
	if seenKey() != "stored-key" {
		t.Errorf("expected the stored key to be used, got %q", seenKey())
	}
}

func TestTestComposioKey_EmptyBodyUsesStoredKey(t *testing.T) {
	setupTestDB(t)
	seenKey := fakeComposio(t)

	enc, err := utils.Encrypt("stored-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := database.SetSetting("composio_api_key", enc); err != nil {
		t.Fatalf("set setting: %v", err)
	}

	report := postComposioTest(t, "")
	if !report.OK {
		t.Errorf("expected ok, got %+v", report)
	}
	if seenKey() != "stored-key" {
		t.Errorf("expected the stored key to be used, got %q", seenKey())
	}
}

func TestTestComposioKey_MalformedBody(t *testing.T) {
	setupTestDB(t)
	fakeComposio(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/composio/test", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	TestComposioKey(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for a malformed body, got %d", rec.Code)
	}
}
