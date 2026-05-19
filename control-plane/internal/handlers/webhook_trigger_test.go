package handlers

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
)

// setupWebhookTest provisions an in-memory DB with the schema the webhook
// handlers touch, plus a stub bridge so tests don't need a live OpenClaw
// gateway. Returns the captured bridge invocation; tests configure the
// bridge's reply/error via the returned pointers before calling the
// handler.
type bridgeCall struct {
	called      bool
	instanceID  uint
	sessionName string
	message     string
	attachments []WebhookAttachment
	reply       string
	err         error
}

func setupWebhookTest(t *testing.T) *bridgeCall {
	t.Helper()
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.WebhookApiKey{}, &database.WebhookLog{}); err != nil {
		t.Fatalf("automigrate webhook tables: %v", err)
	}

	call := &bridgeCall{}
	orig := runWebhookBridge
	runWebhookBridge = func(ctx context.Context, instanceID uint, sessionName, message string, attachments []WebhookAttachment) (string, error) {
		call.called = true
		call.instanceID = instanceID
		call.sessionName = sessionName
		call.message = message
		call.attachments = attachments
		return call.reply, call.err
	}
	t.Cleanup(func() { runWebhookBridge = orig })
	return call
}

func createTestInstanceWithUUID(t *testing.T, uuid string) database.Instance {
	t.Helper()
	inst := database.Instance{
		UUID:        uuid,
		Name:        "bot-" + uuid,
		DisplayName: "Test " + uuid,
		Status:      "running",
	}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	return inst
}

func createWebhookKey(t *testing.T, instanceID uint, raw string, isPrivate bool) database.WebhookApiKey {
	t.Helper()
	enc, err := utils.Encrypt(raw)
	if err != nil {
		t.Fatalf("encrypt webhook key: %v", err)
	}
	k := database.WebhookApiKey{
		InstanceID: instanceID,
		Key:        enc,
		Label:      "test",
		IsPrivate:  isPrivate,
	}
	if err := database.DB.Create(&k).Error; err != nil {
		t.Fatalf("create webhook key: %v", err)
	}
	return k
}

// newPublicWebhookRequest builds a POST /webhooks/{uuid} request for the
// PublicWebhookTrigger handler, with the chi route ctx populated so the
// handler can look up the UUID via chi.URLParam.
func newPublicWebhookRequest(uuid, bearer, body, contentType string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/"+uuid, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("uuid", uuid)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func countLogs(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := database.DB.Model(&database.WebhookLog{}).Count(&n).Error; err != nil {
		t.Fatalf("count logs: %v", err)
	}
	return n
}

func latestLog(t *testing.T) database.WebhookLog {
	t.Helper()
	var log database.WebhookLog
	if err := database.DB.Order("id DESC").First(&log).Error; err != nil {
		t.Fatalf("fetch latest log: %v", err)
	}
	return log
}

// --- Public endpoint tests ---

func TestPublicWebhookTrigger_MissingUUID(t *testing.T) {
	setupWebhookTest(t)
	// Build a request whose chi route context has no "uuid" param.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if got := countLogs(t); got != 0 {
		t.Fatalf("log rows = %d, want 0 on missing-uuid", got)
	}
}

func TestPublicWebhookTrigger_UnknownInstance(t *testing.T) {
	setupWebhookTest(t)
	req := newPublicWebhookRequest("does-not-exist", "anything", "", "")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if got := countLogs(t); got != 0 {
		t.Fatalf("log rows = %d, want 0 (instance miss is silent)", got)
	}
}

func TestPublicWebhookTrigger_NoKeysConfigured(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-no-keys")
	req := newPublicWebhookRequest(inst.UUID, "anything", "", "")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	log := latestLog(t)
	if log.StatusCode != http.StatusNotFound {
		t.Fatalf("log status = %d, want 404", log.StatusCode)
	}
	if log.ErrorMessage != "no keys configured for endpoint" {
		t.Fatalf("log error = %q", log.ErrorMessage)
	}
	if log.IsPrivate {
		t.Fatalf("log.IsPrivate=true, want false on public endpoint")
	}
}

func TestPublicWebhookTrigger_MissingBearer(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-keys-but-no-bearer")
	createWebhookKey(t, inst.ID, "secret-key-abcd1234", false)

	req := newPublicWebhookRequest(inst.UUID, "", "", "")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	log := latestLog(t)
	if log.StatusCode != http.StatusUnauthorized {
		t.Fatalf("log status = %d, want 401", log.StatusCode)
	}
	if log.ErrorMessage != "invalid api key" {
		t.Fatalf("log error = %q", log.ErrorMessage)
	}
	if log.KeyLast4 != "" {
		t.Fatalf("log.KeyLast4 = %q, want empty", log.KeyLast4)
	}
}

func TestPublicWebhookTrigger_WrongBearer(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-wrong-bearer")
	createWebhookKey(t, inst.ID, "the-real-secret-key-1234", false)

	req := newPublicWebhookRequest(inst.UUID, "the-real-secret-key-WRONG", "", "")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPublicWebhookTrigger_NonBearerScheme(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-non-bearer")
	createWebhookKey(t, inst.ID, "secret-key-abcd1234", false)

	req := newPublicWebhookRequest(inst.UUID, "", "", "")
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPublicWebhookTrigger_PrivateKeyRejectedOnPublicEndpoint(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-private-only")
	// Only a PRIVATE key exists.
	createWebhookKey(t, inst.ID, "private-only-key-9999", true)

	// Caller presents the private key against the public endpoint.
	req := newPublicWebhookRequest(inst.UUID, "private-only-key-9999", "", "")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	// matchWebhookKey finds the key, but key.IsPrivate != isPrivate, so the
	// scope mismatch branch fires. count(is_private=false) == 0 so we end
	// up in the 404 "no keys configured" branch.
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestPublicWebhookTrigger_MissingSessionName(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-missing-session")
	createWebhookKey(t, inst.ID, "secret-key-abcd1234", false)

	req := newPublicWebhookRequest(inst.UUID, "secret-key-abcd1234", `{"message":"hi"}`, "application/json")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	log := latestLog(t)
	if log.StatusCode != http.StatusBadRequest {
		t.Fatalf("log status = %d, want 400", log.StatusCode)
	}
	if log.ErrorMessage != "session_name required" {
		t.Fatalf("log error = %q", log.ErrorMessage)
	}
}

func TestPublicWebhookTrigger_InvalidSessionName(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-invalid-session")
	createWebhookKey(t, inst.ID, "secret-key-abcd1234", false)

	bad := []string{"has space", "has/slash", "has\\backslash", "héllo"}
	for _, name := range bad {
		body := fmt.Sprintf(`{"session_name":%q,"message":"hi"}`, name)
		req := newPublicWebhookRequest(inst.UUID, "secret-key-abcd1234", body, "application/json")
		w := httptest.NewRecorder()
		PublicWebhookTrigger(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("name=%q: status = %d, want 400", name, w.Code)
		}
		log := latestLog(t)
		if log.ErrorMessage != "invalid session_name" {
			t.Fatalf("name=%q: log error = %q", name, log.ErrorMessage)
		}
	}
}

func TestPublicWebhookTrigger_MalformedJSON(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-malformed")
	createWebhookKey(t, inst.ID, "secret-key-abcd1234", false)

	req := newPublicWebhookRequest(inst.UUID, "secret-key-abcd1234", `{not json`, "application/json")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	log := latestLog(t)
	if log.StatusCode != http.StatusBadRequest {
		t.Fatalf("log status = %d, want 400", log.StatusCode)
	}
	if log.ErrorMessage == "" {
		t.Fatalf("expected non-empty error message on malformed json")
	}
	if log.RequestBytes <= 0 {
		t.Fatalf("RequestBytes = %d, want >0", log.RequestBytes)
	}
}

func TestPublicWebhookTrigger_HappyJSON(t *testing.T) {
	call := setupWebhookTest(t)
	call.reply = "hello world"

	inst := createTestInstanceWithUUID(t, "uuid-happy-json")
	keyRaw := "live-key-abcdef1234"
	createWebhookKey(t, inst.ID, keyRaw, false)

	body := `{"session_name":"sess.1","message":"do thing"}`
	req := newPublicWebhookRequest(inst.UUID, keyRaw, body, "application/json")
	w := httptest.NewRecorder()
	beforeCall := time.Now().UTC()
	PublicWebhookTrigger(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	if w.Body.String() != "hello world" {
		t.Fatalf("body = %q, want %q", w.Body.String(), "hello world")
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}

	if !call.called {
		t.Fatalf("bridge was not invoked")
	}
	if call.instanceID != inst.ID {
		t.Fatalf("bridge instanceID = %d, want %d", call.instanceID, inst.ID)
	}
	if call.sessionName != "sess.1" {
		t.Fatalf("bridge sessionName = %q", call.sessionName)
	}
	if call.message != "do thing" {
		t.Fatalf("bridge message = %q", call.message)
	}

	log := latestLog(t)
	if log.StatusCode != http.StatusOK {
		t.Fatalf("log status = %d, want 200", log.StatusCode)
	}
	if log.SessionName != "sess.1" {
		t.Fatalf("log session = %q", log.SessionName)
	}
	if log.KeyLast4 != "1234" {
		t.Fatalf("log KeyLast4 = %q, want 1234", log.KeyLast4)
	}
	if log.RequestBytes != len(body) {
		t.Fatalf("log RequestBytes = %d, want %d", log.RequestBytes, len(body))
	}
	if log.ResponseBytes != len("hello world") {
		t.Fatalf("log ResponseBytes = %d, want %d", log.ResponseBytes, len("hello world"))
	}
	if log.DurationMs < 0 {
		t.Fatalf("log DurationMs = %d, want >=0", log.DurationMs)
	}
	if log.IsPrivate {
		t.Fatalf("log.IsPrivate=true on public endpoint")
	}

	// LastUsedAt should be bumped on the matched key.
	var k database.WebhookApiKey
	if err := database.DB.Where("instance_id = ?", inst.ID).First(&k).Error; err != nil {
		t.Fatalf("reload key: %v", err)
	}
	if k.LastUsedAt == nil {
		t.Fatalf("LastUsedAt not set")
	}
	if k.LastUsedAt.Before(beforeCall.Add(-time.Second)) {
		t.Fatalf("LastUsedAt = %v, want >= %v", k.LastUsedAt, beforeCall)
	}
}

func TestPublicWebhookTrigger_HappyMultipart(t *testing.T) {
	call := setupWebhookTest(t)
	call.reply = "done"

	inst := createTestInstanceWithUUID(t, "uuid-multipart")
	keyRaw := "mp-key-aaaabbbb"
	createWebhookKey(t, inst.ID, keyRaw, false)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("session_name", "mp.session"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.WriteField("message", "with files"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	fw1, _ := mw.CreateFormFile("file1", "a.txt")
	fw1.Write([]byte("alpha"))
	fw2, _ := mw.CreateFormFile("file2", "b.txt")
	fw2.Write([]byte("bravo!!"))
	mw.Close()

	req := newPublicWebhookRequest(inst.UUID, keyRaw, buf.String(), mw.FormDataContentType())
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	if !call.called {
		t.Fatalf("bridge not invoked")
	}
	if call.sessionName != "mp.session" || call.message != "with files" {
		t.Fatalf("bridge params: session=%q message=%q", call.sessionName, call.message)
	}
	if len(call.attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(call.attachments))
	}
	got := map[string]string{}
	for _, a := range call.attachments {
		got[a.Filename] = string(a.Content)
	}
	if got["a.txt"] != "alpha" || got["b.txt"] != "bravo!!" {
		t.Fatalf("attachment contents = %v", got)
	}

	log := latestLog(t)
	expectedBytes := len("alpha") + len("bravo!!")
	if log.RequestBytes != expectedBytes {
		t.Fatalf("log RequestBytes = %d, want %d (file bytes only for multipart)", log.RequestBytes, expectedBytes)
	}
}

func TestPublicWebhookTrigger_BridgeError(t *testing.T) {
	call := setupWebhookTest(t)
	call.err = fmt.Errorf("dial gateway: connection refused")

	inst := createTestInstanceWithUUID(t, "uuid-bridge-err")
	keyRaw := "err-key-zzzz9999"
	createWebhookKey(t, inst.ID, keyRaw, false)

	body := `{"session_name":"s","message":"m"}`
	req := newPublicWebhookRequest(inst.UUID, keyRaw, body, "application/json")
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if !strings.HasPrefix(w.Body.String(), "agent error: ") {
		t.Fatalf("body = %q, expected agent error prefix", w.Body.String())
	}
	log := latestLog(t)
	if log.StatusCode != http.StatusBadGateway {
		t.Fatalf("log status = %d, want 502", log.StatusCode)
	}
	if !strings.Contains(log.ErrorMessage, "dial gateway") {
		t.Fatalf("log error = %q", log.ErrorMessage)
	}
}

func TestPublicWebhookTrigger_SourceIPFromXFF(t *testing.T) {
	call := setupWebhookTest(t)
	call.reply = "ok"

	inst := createTestInstanceWithUUID(t, "uuid-xff")
	keyRaw := "xff-key-aaaabbbb"
	createWebhookKey(t, inst.ID, keyRaw, false)

	req := newPublicWebhookRequest(inst.UUID, keyRaw, `{"session_name":"s","message":"m"}`, "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	PublicWebhookTrigger(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	log := latestLog(t)
	if log.SourceIP != "203.0.113.7" {
		t.Fatalf("SourceIP = %q, want 203.0.113.7", log.SourceIP)
	}
}

func TestPublicWebhookTrigger_OneLogPerCall(t *testing.T) {
	call := setupWebhookTest(t)
	call.reply = "ok"

	inst := createTestInstanceWithUUID(t, "uuid-one-log")
	keyRaw := "log-key-aaaabbbb"
	createWebhookKey(t, inst.ID, keyRaw, false)

	for i := 0; i < 3; i++ {
		req := newPublicWebhookRequest(inst.UUID, keyRaw, `{"session_name":"s","message":"m"}`, "application/json")
		w := httptest.NewRecorder()
		PublicWebhookTrigger(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("iter %d: status = %d", i, w.Code)
		}
	}
	if got := countLogs(t); got != 3 {
		t.Fatalf("log rows = %d, want 3", got)
	}
}

// --- Private endpoint tests ---

func TestPrivateWebhookTrigger_EmptyUUIDPath(t *testing.T) {
	setupWebhookTest(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/", nil)
	w := httptest.NewRecorder()
	PrivateWebhookTrigger(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestPrivateWebhookTrigger_WrongPrefix(t *testing.T) {
	setupWebhookTest(t)
	req := httptest.NewRequest(http.MethodPost, "/elsewhere/xxx", nil)
	w := httptest.NewRecorder()
	PrivateWebhookTrigger(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestPrivateWebhookTrigger_HappyPath(t *testing.T) {
	call := setupWebhookTest(t)
	call.reply = "private-ok"

	inst := createTestInstanceWithUUID(t, "uuid-priv-happy")
	keyRaw := "priv-key-aaaabbbb"
	createWebhookKey(t, inst.ID, keyRaw, true)

	body := `{"session_name":"p1","message":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/"+inst.UUID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+keyRaw)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	PrivateWebhookTrigger(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
	if w.Body.String() != "private-ok" {
		t.Fatalf("body = %q", w.Body.String())
	}
	if !call.called {
		t.Fatalf("bridge not invoked")
	}
	log := latestLog(t)
	if !log.IsPrivate {
		t.Fatalf("log.IsPrivate=false, want true on private endpoint")
	}
}

func TestPrivateWebhookTrigger_TrailingPathSegments(t *testing.T) {
	call := setupWebhookTest(t)
	call.reply = "ok"

	inst := createTestInstanceWithUUID(t, "uuid-priv-trailing")
	keyRaw := "priv-trail-aaaabbbb"
	createWebhookKey(t, inst.ID, keyRaw, true)

	body := `{"session_name":"p1","message":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/"+inst.UUID+"/extra/segments", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+keyRaw)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	PrivateWebhookTrigger(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", w.Code, w.Body.String())
	}
}

func TestPrivateWebhookTrigger_PublicKeyRejected(t *testing.T) {
	setupWebhookTest(t)
	inst := createTestInstanceWithUUID(t, "uuid-priv-scope")
	keyRaw := "pub-only-key-aaaabbbb"
	createWebhookKey(t, inst.ID, keyRaw, false)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/"+inst.UUID, strings.NewReader(`{"session_name":"s","message":"m"}`))
	req.Header.Set("Authorization", "Bearer "+keyRaw)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	PrivateWebhookTrigger(w, req)

	// Only public keys exist; private endpoint sees zero eligible keys → 404.
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
