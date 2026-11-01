package browserauthn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------- fake CDP

type fakeCall struct {
	Session string
	Method  string
}

type fakeBrowser struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	conn        *websocket.Conn
	initialTabs int
	nextSession int
	nextAuth    int
	calls       []fakeCall
	authsBySess map[string][]string              // session → authenticator ids
	creds       map[string]map[string]credential // authenticator → credId → cred
	connects    atomic.Int32
}

func newFakeBrowser(t *testing.T, initialTabs int) *fakeBrowser {
	f := &fakeBrowser{
		t:           t,
		initialTabs: initialTabs,
		authsBySess: map[string][]string{},
		creds:       map[string]map[string]credential{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/x"}`))
	})
	mux.HandleFunc("/devtools/browser/x", f.serveWS)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBrowser) dial(ctx context.Context, _ uint) (io.ReadWriteCloser, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", strings.TrimPrefix(f.srv.URL, "http://"))
}

func (f *fakeBrowser) serveWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	f.connects.Add(1)
	f.mu.Lock()
	f.conn = c
	// A reconnect means a fresh CDP client: per-client authenticators are gone.
	f.authsBySess = map[string][]string{}
	f.creds = map[string]map[string]credential{}
	f.mu.Unlock()
	ctx := r.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
			SessionID string          `json:"sessionId"`
		}
		_ = json.Unmarshal(data, &m)
		result, after := f.handle(m.SessionID, m.Method, m.Params)
		resp, _ := json.Marshal(map[string]interface{}{"id": m.ID, "result": result, "sessionId": m.SessionID})
		_ = c.Write(ctx, websocket.MessageText, resp)
		if after != nil {
			after()
		}
	}
}

func (f *fakeBrowser) handle(sess, method string, params json.RawMessage) (interface{}, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{sess, method})
	switch method {
	case "Target.getTargets":
		infos := []map[string]string{{"type": "browser"}}
		for i := 0; i < f.initialTabs; i++ {
			infos = append(infos, map[string]string{"type": "page"})
		}
		return map[string]interface{}{"targetInfos": infos}, nil
	case "Target.setAutoAttach":
		n := f.initialTabs
		return map[string]interface{}{}, func() {
			for i := 0; i < n; i++ {
				f.attach(false)
			}
		}
	case "WebAuthn.addVirtualAuthenticator":
		f.nextAuth++
		id := fmt.Sprintf("auth-%d", f.nextAuth)
		f.authsBySess[sess] = append(f.authsBySess[sess], id)
		f.creds[id] = map[string]credential{}
		return map[string]string{"authenticatorId": id}, nil
	case "WebAuthn.addCredential":
		var p struct {
			AuthenticatorID string     `json:"authenticatorId"`
			Credential      credential `json:"credential"`
		}
		_ = json.Unmarshal(params, &p)
		f.creds[p.AuthenticatorID][p.Credential.CredentialID] = p.Credential
	case "WebAuthn.removeCredential":
		var p struct {
			AuthenticatorID string `json:"authenticatorId"`
			CredentialID    string `json:"credentialId"`
		}
		_ = json.Unmarshal(params, &p)
		delete(f.creds[p.AuthenticatorID], p.CredentialID)
	case "WebAuthn.getCredentials":
		var p struct {
			AuthenticatorID string `json:"authenticatorId"`
		}
		_ = json.Unmarshal(params, &p)
		out := []credential{}
		for _, c := range f.creds[p.AuthenticatorID] {
			out = append(out, c)
		}
		return map[string]interface{}{"credentials": out}, nil
	}
	return map[string]interface{}{}, nil
}

// attach emits Target.attachedToTarget for a new page. Caller may hold f.mu
// only via handle's deferred callbacks (which run unlocked).
func (f *fakeBrowser) attach(waiting bool) string {
	f.mu.Lock()
	f.nextSession++
	sess := fmt.Sprintf("S%d", f.nextSession)
	c := f.conn
	f.mu.Unlock()
	f.emit(c, "", "Target.attachedToTarget", map[string]interface{}{
		"sessionId": sess, "waitingForDebugger": waiting,
		"targetInfo": map[string]string{"type": "page"},
	})
	return sess
}

func (f *fakeBrowser) emit(c *websocket.Conn, sess, method string, params interface{}) {
	msg := map[string]interface{}{"method": method, "params": params}
	if sess != "" {
		msg["sessionId"] = sess
	}
	data, _ := json.Marshal(msg)
	_ = c.Write(context.Background(), websocket.MessageText, data)
}

// register simulates a site creating a passkey in a tab's first authenticator.
func (f *fakeBrowser) register(sess string, c credential) {
	f.mu.Lock()
	auth := f.authsBySess[sess][0]
	f.creds[auth][c.CredentialID] = c
	conn := f.conn
	f.mu.Unlock()
	f.emit(conn, sess, "WebAuthn.credentialAdded", map[string]interface{}{"authenticatorId": auth, "credential": c})
}

func (f *fakeBrowser) assertion(sess, credID string, signCount uint32) {
	f.mu.Lock()
	auth := f.authsBySess[sess][0]
	c := f.creds[auth][credID]
	c.SignCount = signCount
	f.creds[auth][credID] = c
	conn := f.conn
	f.mu.Unlock()
	f.emit(conn, sess, "WebAuthn.credentialAsserted", map[string]interface{}{"authenticatorId": auth, "credential": c})
}

func (f *fakeBrowser) credsIn(sess string) map[string]credential {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]credential{}
	for _, a := range f.authsBySess[sess] {
		for id, c := range f.creds[a] {
			out[id] = c
		}
	}
	return out
}

func (f *fakeBrowser) callsFor(sess string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.Session == sess {
			out = append(out, c.Method)
		}
	}
	return out
}

func (f *fakeBrowser) dropConnection() {
	f.mu.Lock()
	c := f.conn
	f.mu.Unlock()
	_ = c.CloseNow()
}

// ---------------------------------------------------------------- helpers

func setupDB(t *testing.T, setting string) uint {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Instance{}, &database.Setting{}, &database.BrowserSession{}, &database.BrowserPasskey{}); err != nil {
		t.Fatal(err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })
	inst := database.Instance{Name: "bot-a", DisplayName: "a", ContainerImage: "claworc/openclaw:latest", VirtualAuthenticator: setting}
	if err := db.Create(&inst).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.BrowserSession{InstanceID: inst.ID, Status: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	return inst.ID
}

func storedPasskey(t *testing.T, instanceID uint, kind Kind, id string, signCount uint32) {
	t.Helper()
	row, err := toRow(instanceID, kind, credential{CredentialID: id, RpID: "example.com", PrivateKey: "PRIV-" + id, IsResidentCredential: true, SignCount: signCount})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertBrowserPasskey(row); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func newTestManager(f *fakeBrowser, running *atomic.Bool) *Manager {
	m := New(f.dial, func(context.Context, uint) bool { return running.Load() })
	m.ReconcileInterval = 100 * time.Millisecond
	// Registered after setupDB's cleanup, so it runs first (LIFO): sessions
	// are fully stopped before the test DB is torn down.
	f.t.Cleanup(m.stopAll)
	return m
}

// ---------------------------------------------------------------- tests

func TestStart_InstallsAuthenticatorAndStoredCredentialsBeforeResume(t *testing.T) {
	id := setupDB(t, SettingPlatform)
	storedPasskey(t, id, KindPlatform, "cred-1", 4)
	f := newFakeBrowser(t, 1)
	var running atomic.Bool
	running.Store(true)
	m := newTestManager(f, &running)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Pre-existing tab is configured by the time Start returns.
	got := f.credsIn("S1")
	if c, ok := got["cred-1"]; !ok || c.PrivateKey != "PRIV-cred-1" || c.SignCount != 4 {
		t.Fatalf("stored credential not injected into existing tab: %+v", got)
	}

	// New tab: enable → add authenticator → add credential → resume, in order.
	sess := f.attach(true)
	eventually(t, "new tab resumed", func() bool {
		calls := f.callsFor(sess)
		return len(calls) > 0 && calls[len(calls)-1] == "Runtime.runIfWaitingForDebugger"
	})
	want := []string{"WebAuthn.enable", "WebAuthn.addVirtualAuthenticator", "WebAuthn.addCredential", "Runtime.runIfWaitingForDebugger"}
	if calls := f.callsFor(sess); strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", calls, want)
	}
	m.Stop(id)
}

func TestCredentialAdded_PersistsEncryptedAndFansOut(t *testing.T) {
	id := setupDB(t, SettingBoth)
	f := newFakeBrowser(t, 2)
	var running atomic.Bool
	running.Store(true)
	m := newTestManager(f, &running)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(id)

	f.register("S1", credential{CredentialID: "new-1", RpID: "github.com", PrivateKey: "SECRET", IsResidentCredential: true, UserName: "octo", SignCount: 0})

	eventually(t, "credential persisted", func() bool {
		rows, _ := database.ListBrowserPasskeys(id)
		return len(rows) == 1
	})
	rows, _ := database.ListBrowserPasskeys(id)
	if rows[0].PrivateKey == "SECRET" || rows[0].PrivateKey == "" {
		t.Fatalf("private key not encrypted at rest: %q", rows[0].PrivateKey)
	}
	if pk, _ := utils.Decrypt(rows[0].PrivateKey); pk != "SECRET" {
		t.Fatalf("decrypt mismatch: %q", pk)
	}
	if rows[0].Authenticator != string(KindPlatform) || rows[0].RpID != "github.com" || rows[0].UserName != "octo" {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
	eventually(t, "fan-out to second tab", func() bool { _, ok := f.credsIn("S2")["new-1"]; return ok })
}

func TestCredentialAsserted_AdvancesSignCountEverywhere(t *testing.T) {
	id := setupDB(t, SettingPlatform)
	storedPasskey(t, id, KindPlatform, "c", 1)
	f := newFakeBrowser(t, 2)
	var running atomic.Bool
	running.Store(true)
	m := newTestManager(f, &running)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(id)

	f.assertion("S1", "c", 7)
	eventually(t, "sign count persisted", func() bool {
		rows, _ := database.ListBrowserPasskeys(id)
		return len(rows) == 1 && rows[0].SignCount == 7 && rows[0].LastUsedAt != nil
	})
	eventually(t, "other tab refreshed", func() bool { return f.credsIn("S2")["c"].SignCount == 7 })
}

func TestReconcile_PicksUpMissedCredentials(t *testing.T) {
	id := setupDB(t, SettingSecurityKey)
	f := newFakeBrowser(t, 1)
	var running atomic.Bool
	running.Store(true)
	m := newTestManager(f, &running)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(id)

	// Credential appears in the authenticator without any event.
	f.mu.Lock()
	auth := f.authsBySess["S1"][0]
	f.creds[auth]["silent"] = credential{CredentialID: "silent", RpID: "x.com", PrivateKey: "K", SignCount: 3}
	f.mu.Unlock()

	eventually(t, "reconcile persisted silent credential", func() bool {
		rows, _ := database.ListBrowserPasskeys(id)
		return len(rows) == 1 && rows[0].CredentialID == "silent" && rows[0].Authenticator == string(KindSecurityKey)
	})
}

func TestReconnect_ReinstallsPersistedCredentials(t *testing.T) {
	id := setupDB(t, SettingPlatform)
	f := newFakeBrowser(t, 1)
	var running atomic.Bool
	running.Store(true)
	m := newTestManager(f, &running)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(id)

	f.register("S1", credential{CredentialID: "keep", RpID: "a.com", PrivateKey: "P", SignCount: 0})
	eventually(t, "persisted", func() bool { rows, _ := database.ListBrowserPasskeys(id); return len(rows) == 1 })

	// Browser restarts: connection drops, fresh CDP state, one tab again.
	f.dropConnection()
	eventually(t, "reconnected", func() bool { return f.connects.Load() >= 2 })
	eventually(t, "credential reinstalled after reconnect", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, a := range f.creds {
			if _, ok := a["keep"]; ok {
				return true
			}
		}
		return false
	})
}

func TestStop_WhenSessionNotRunningDoesNotReconnect(t *testing.T) {
	id := setupDB(t, SettingPlatform)
	f := newFakeBrowser(t, 0)
	var running atomic.Bool
	running.Store(true)
	m := newTestManager(f, &running)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	running.Store(false)
	f.dropConnection()
	eventually(t, "session forgotten", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.sessions) == 0
	})
	time.Sleep(1500 * time.Millisecond)
	if n := f.connects.Load(); n != 1 {
		t.Fatalf("reconnected %d times after session stopped", n-1)
	}
}

func TestStart_SettingOffIsNoop(t *testing.T) {
	id := setupDB(t, SettingOff)
	f := newFakeBrowser(t, 1)
	var running atomic.Bool
	running.Store(true)
	m := newTestManager(f, &running)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if f.connects.Load() != 0 {
		t.Fatal("connected to CDP although authenticator is off")
	}
}

func TestKindsFor(t *testing.T) {
	if k := KindsFor(SettingBoth); len(k) != 2 {
		t.Fatalf("both: %v", k)
	}
	if k := KindsFor("bogus"); k != nil {
		t.Fatalf("bogus: %v", k)
	}
	if ValidSetting("bogus") || !ValidSetting("") {
		t.Fatal("ValidSetting")
	}
}
