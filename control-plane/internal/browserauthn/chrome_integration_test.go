package browserauthn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// TestRealChrome exercises the manager against a real headless Chrome: a
// passkey created in one browser process must still sign in after the
// browser is replaced by a fresh one (fresh profile = simulated pod restart).
//
//	CLAWORC_TEST_CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
//	  go test -vet=off -run TestRealChrome ./internal/browserauthn/
func TestRealChrome(t *testing.T) {
	t.Run("fingerprint", func(t *testing.T) { realChromeRoundTrip(t, SettingPlatform, "platform") })
	t.Run("security_key", func(t *testing.T) { realChromeRoundTrip(t, SettingSecurityKey, "cross-platform") })
}

func realChromeRoundTrip(t *testing.T, setting, attachment string) {
	bin := os.Getenv("CLAWORC_TEST_CHROME")
	if bin == "" {
		t.Skip("set CLAWORC_TEST_CHROME to a Chrome/Chromium binary to run")
	}
	id := setupDB(t, setting)

	// Relying party page on localhost (a secure context; IPs can't be RP IDs).
	rp := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>rp</title>ok"))
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rp.Listener = ln
	rp.Start()
	defer rp.Close()
	rpURL := fmt.Sprintf("http://localhost:%d/", ln.Addr().(*net.TCPAddr).Port)

	var port atomic.Int32
	dial := func(ctx context.Context, _ uint) (io.ReadWriteCloser, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(int(port.Load())))
	}
	var running atomic.Bool
	running.Store(true)
	m := New(dial, func(context.Context, uint) bool { return running.Load() })
	t.Cleanup(m.stopAll)

	// ---- browser #1: register a passkey
	stop1 := launchChrome(t, bin, &port)
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	credID := runInNewTab(t, dial, rpURL, `const attachment = "`+attachment+`";`+`
		(async () => {
		  const c = await navigator.credentials.create({publicKey: {
		    rp: {name: "Test RP"},
		    user: {id: new Uint8Array([1,2,3,4]), name: "alice", displayName: "Alice"},
		    challenge: new Uint8Array(32),
		    pubKeyCredParams: [{type: "public-key", alg: -7}],
		    authenticatorSelection: {authenticatorAttachment: attachment, residentKey: "required", userVerification: "required"},
		  }});
		  return c.id;
		})()`)
	if credID == "" {
		t.Fatal("no credential id returned")
	}
	eventually(t, "credential persisted", func() bool {
		rows, _ := database.ListBrowserPasskeys(id)
		return len(rows) == 1 && rows[0].RpID == "localhost" && rows[0].UserName == "alice" &&
			rows[0].Authenticator == string(KindsFor(setting)[0])
	})

	// ---- pod restart: Chrome dies, a brand-new Chrome with an empty profile comes up
	running.Store(false)
	stop1()
	m.Stop(id)
	running.Store(true)
	stop2 := launchChrome(t, bin, &port)
	defer stop2()
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatalf("Start #2: %v", err)
	}

	// Discoverable sign-in (no allowCredentials) must find the persisted passkey.
	got := runInNewTab(t, dial, rpURL, `
		(async () => {
		  const a = await navigator.credentials.get({publicKey: {
		    challenge: new Uint8Array(32), userVerification: "required",
		  }});
		  return a.id;
		})()`)
	if got != credID {
		t.Fatalf("assertion used credential %q, want %q", got, credID)
	}
	eventually(t, "sign count advanced", func() bool {
		rows, _ := database.ListBrowserPasskeys(id)
		return len(rows) == 1 && rows[0].LastUsedAt != nil && rows[0].SignCount >= 1
	})
}

func launchChrome(t *testing.T, bin string, port *atomic.Int32) func() {
	t.Helper()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(bin, "--headless=new", "--no-first-run", "--no-default-browser-check",
		"--remote-debugging-address=127.0.0.1", "--remote-debugging-port="+strconv.Itoa(p),
		"--user-data-dir="+t.TempDir(), "about:blank")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	port.Store(int32(p))
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", p)); err == nil {
			resp.Body.Close()
			return func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Fatal("chrome did not start")
	return nil
}

// runInNewTab plays the agent: a separate CDP client opens a tab, navigates to
// url and evaluates a promise-returning expression, returning its string value.
func runInNewTab(t *testing.T, dial DialFunc, url, expr string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := dialCDP(ctx, dial, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		for range c.Events() {
		}
	}()
	var tgt struct {
		TargetID string `json:"targetId"`
	}
	if err := c.Call(ctx, "", "Target.createTarget", map[string]string{"url": url}, &tgt); err != nil {
		t.Fatal(err)
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.Call(ctx, "", "Target.attachToTarget", map[string]interface{}{"targetId": tgt.TargetID, "flatten": true}, &att); err != nil {
		t.Fatal(err)
	}
	// Wait for the page to load.
	for i := 0; i < 100; i++ {
		var r struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		_ = c.Call(ctx, att.SessionID, "Runtime.evaluate", map[string]interface{}{"expression": "document.readyState + location.host"}, &r)
		if len(r.Result.Value) > 8 && r.Result.Value[:8] == "complete" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := c.Call(ctx, att.SessionID, "Runtime.evaluate", map[string]interface{}{
		"expression": expr, "awaitPromise": true, "returnByValue": true, "userGesture": true,
	}, &res); err != nil {
		t.Fatal(err)
	}
	if res.ExceptionDetails != nil {
		t.Fatalf("page script failed: %s %s", res.ExceptionDetails.Text, res.ExceptionDetails.Exception.Description)
	}
	var s string
	_ = json.Unmarshal(res.Result.Value, &s)
	return s
}
