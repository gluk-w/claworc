package outboundproxy

import (
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&database.Setting{}, &database.Instance{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })
}

func mustEncrypt(t *testing.T, s string) string {
	t.Helper()
	enc, err := utils.Encrypt(s)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return enc
}

func TestNormalize(t *testing.T) {
	ok := map[string]string{
		"":                                     "",
		"  http://u:p@proxy.example.com:9000 ": "http://u:p@proxy.example.com:9000",
		"HTTP://proxy.example.com:8080/":       "http://proxy.example.com:8080",
		"socks5://user:pa%40ss@1.2.3.4:1080":   "socks5://user:pa%40ss@1.2.3.4:1080",
		"socks5h://h:1":                        "socks5h://h:1",
		"https://[::1]:443":                    "https://[::1]:443",
	}
	for in, want := range ok {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"proxy.example.com:9000",        // no scheme
		"ftp://proxy.example.com:21",    // unsupported scheme
		"http://proxy.example.com",      // no port
		"http://proxy.example.com:80/x", // path
		"http://h:1?x=y",                // query
	} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) succeeded, want error", bad)
		}
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		"":                                   "",
		"http://proxy.example.com:9000":      "http://proxy.example.com:9000",
		"http://user@proxy.example.com:9000": "http://user@proxy.example.com:9000",
		"http://user:secret@h:9000":          "http://user:****@h:9000",
		"socks5://u:p%40ss@h:1080":           "socks5://u:****@h:1080",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestPrepareUpdate(t *testing.T) {
	setupDB(t)
	current := mustEncrypt(t, "http://user:secret@h:9000")

	// Echoing back the masked value keeps the stored ciphertext.
	enc, changed, err := PrepareUpdate("http://user:****@h:9000", current)
	if err != nil || changed || enc != current {
		t.Fatalf("masked echo: enc=%q changed=%v err=%v", enc, changed, err)
	}
	// Same plaintext is not a change.
	if _, changed, _ := PrepareUpdate("http://user:secret@h:9000", current); changed {
		t.Fatal("identical value reported as changed")
	}
	// A new value is encrypted.
	enc, changed, err = PrepareUpdate("http://user:other@h:9000", current)
	if err != nil || !changed || Decrypt(enc) != "http://user:other@h:9000" {
		t.Fatalf("new value: enc=%q changed=%v err=%v", enc, changed, err)
	}
	// Clearing.
	enc, changed, err = PrepareUpdate("", current)
	if err != nil || !changed || enc != "" {
		t.Fatalf("clear: enc=%q changed=%v err=%v", enc, changed, err)
	}
	// Invalid.
	if _, _, err := PrepareUpdate("nonsense", current); err == nil {
		t.Fatal("invalid value accepted")
	}
}

func TestEffective(t *testing.T) {
	setupDB(t)
	global := "http://g:gp@global:1"
	custom := "socks5://c:cp@custom:2"
	if err := database.SetSetting(SettingKey, mustEncrypt(t, global)); err != nil {
		t.Fatal(err)
	}
	customEnc := mustEncrypt(t, custom)
	cases := []struct {
		mode, url, want string
	}{
		{"", "", global},
		{ModeInherit, customEnc, global},
		{ModeCustom, customEnc, custom},
		{ModeOff, customEnc, ""},
	}
	for _, c := range cases {
		got := Effective(database.Instance{ProxyMode: c.mode, ProxyURL: c.url})
		if got != c.want {
			t.Errorf("Effective(mode=%q) = %q; want %q", c.mode, got, c.want)
		}
	}
}

func TestApplyAgentEnv(t *testing.T) {
	env := map[string]string{"NO_PROXY": "internal.example.com,localhost", "FOO": "bar"}
	ApplyAgentEnv(env, "http://u:p@h:1")
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if env[k] != "http://u:p@h:1" {
			t.Errorf("%s = %q", k, env[k])
		}
	}
	wantNoProxy := NoProxy + ",internal.example.com"
	if env["NO_PROXY"] != wantNoProxy || env["no_proxy"] != wantNoProxy {
		t.Errorf("NO_PROXY = %q / %q; want %q", env["NO_PROXY"], env["no_proxy"], wantNoProxy)
	}
	if env["NODE_USE_ENV_PROXY"] != "1" || env["FOO"] != "bar" {
		t.Errorf("unexpected env: %v", env)
	}

	// No proxy configured: user-set values are left alone.
	env = map[string]string{"HTTP_PROXY": "http://mine:1"}
	ApplyAgentEnv(env, "")
	if len(env) != 1 || env["HTTP_PROXY"] != "http://mine:1" {
		t.Errorf("empty proxy modified env: %v", env)
	}
}
