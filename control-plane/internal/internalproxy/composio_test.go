package internalproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// setupComposioDB migrates the tables the connections feature touches.
func setupComposioDB(t *testing.T) {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open in-memory DB: %v", err)
	}
	if err := database.DB.AutoMigrate(
		&database.Setting{},
		&database.Instance{},
		&database.ComposioConnection{},
		&database.ComposioAuthConfig{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
}

func mustInstance(t *testing.T, uuid string) database.Instance {
	t.Helper()
	inst := database.Instance{Name: "bot-" + uuid, DisplayName: uuid, UUID: uuid}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return inst
}

func TestEnsureConnectionSecret_Idempotent(t *testing.T) {
	setupComposioDB(t)
	inst := mustInstance(t, "u1")

	s1, gen1, err := EnsureConnectionSecret(inst.ID)
	if err != nil {
		t.Fatalf("EnsureConnectionSecret: %v", err)
	}
	if !gen1 {
		t.Error("expected first call to report generated=true")
	}
	if !strings.HasPrefix(s1, "claworc-cs-") {
		t.Errorf("expected claworc-cs- prefix, got %q", s1)
	}

	s2, gen2, err := EnsureConnectionSecret(inst.ID)
	if err != nil {
		t.Fatalf("EnsureConnectionSecret (2nd): %v", err)
	}
	if gen2 {
		t.Error("expected second call to report generated=false")
	}
	if s1 != s2 {
		t.Error("secret changed across calls")
	}
}

func TestResolveInstanceBySecret(t *testing.T) {
	setupComposioDB(t)
	inst := mustInstance(t, "u2")
	secret, _, err := EnsureConnectionSecret(inst.ID)
	if err != nil {
		t.Fatalf("EnsureConnectionSecret: %v", err)
	}

	got, err := resolveInstanceBySecret(secret)
	if err != nil {
		t.Fatalf("resolveInstanceBySecret: %v", err)
	}
	if got.ID != inst.ID {
		t.Errorf("resolved instance %d, want %d", got.ID, inst.ID)
	}

	if _, err := resolveInstanceBySecret("claworc-cs-bogus"); err == nil {
		t.Error("expected error for unknown secret")
	}
}

func setComposioKey(t *testing.T, key string) {
	t.Helper()
	enc, err := utils.Encrypt(key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	database.SetSetting("composio_api_key", enc)
}

func TestHandleConnections_AuthAndAllowlist(t *testing.T) {
	setupComposioDB(t)
	setComposioKey(t, "real-composio-key")
	inst := mustInstance(t, "u3")
	secret, _, _ := EnsureConnectionSecret(inst.ID)

	// Missing secret → 401
	rec := httptest.NewRecorder()
	HandleConnections(rec, httptest.NewRequest(http.MethodGet, ConnectionsPrefix+"tools", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no-auth: got %d, want 401", rec.Code)
	}

	// Disallowed path → 404
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, ConnectionsPrefix+"connected_accounts", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	HandleConnections(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("disallowed path: got %d, want 404", rec.Code)
	}

	// tools with no active connections → empty list, no upstream call
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, ConnectionsPrefix+"tools", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	HandleConnections(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("empty tools: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleConnections_ExecuteInjectsIdentity(t *testing.T) {
	setupComposioDB(t)
	inst := mustInstance(t, "u4")
	secret, _, _ := EnsureConnectionSecret(inst.ID)

	// Fake Composio upstream that echoes the body and asserts the injected key.
	var gotKey, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"successful":true,"data":{},"error":null}`))
	}))
	defer upstream.Close()
	orig := ComposioBaseURL
	ComposioBaseURL = upstream.URL
	defer func() { ComposioBaseURL = orig }()
	setComposioKey(t, "real-composio-key")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, ConnectionsPrefix+"tools/execute/GMAIL_SEND_EMAIL",
		strings.NewReader(`{"arguments":{"to":"x@y.z"},"user_id":"attacker","connected_account_id":"hijack"}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	HandleConnections(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("execute: got %d body=%s", rec.Code, rec.Body.String())
	}
	if gotKey != "real-composio-key" {
		t.Errorf("upstream x-api-key: got %q, want real key", gotKey)
	}
	if !strings.Contains(gotBody, `"user_id":"claworc-inst-u4"`) {
		t.Errorf("expected server-derived user_id in body, got %s", gotBody)
	}
	if strings.Contains(gotBody, "attacker") || strings.Contains(gotBody, "hijack") {
		t.Errorf("client-supplied identity should be stripped, got %s", gotBody)
	}
}
