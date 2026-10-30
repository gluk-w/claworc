// Package outboundproxy resolves the HTTP/SOCKS5 proxy an instance's outbound
// traffic is routed through (e.g. a residential proxy such as ProxyEmpire) and
// turns it into the env vars consumed by the agent container and browser pod.
//
// The proxy is a single URL, scheme://user:pass@host:port, stored
// Fernet-encrypted. A global default lives in the default_proxy_url setting;
// each instance either inherits it, sets its own, or opts out. See
// docs/proxy.md.
package outboundproxy

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// SettingKey is the settings-table key holding the encrypted global proxy URL.
const SettingKey = "default_proxy_url"

// BrowserEnvKey carries the effective proxy URL into the browser pod, where a
// local forwarder (svc-proxy) authenticates against it on Chromium's behalf.
const BrowserEnvKey = "CLAWORC_BROWSER_PROXY"

// Instance proxy modes.
const (
	ModeInherit = "inherit" // use the global default (also the "" zero value)
	ModeCustom  = "custom"  // use Instance.ProxyURL
	ModeOff     = "off"     // never proxy, even if a global default is set
)

// NoProxy lists destinations that must bypass the proxy. Everything the
// control plane talks to inside the container is on loopback (LLM gateway,
// Composio broker, CDP, agent gateway), reached through SSH tunnels.
const NoProxy = "localhost,127.0.0.1,::1,.svc,.cluster.local"

var allowedSchemes = map[string]bool{"http": true, "https": true, "socks5": true, "socks5h": true}

// ValidMode reports whether m is an accepted instance proxy mode.
func ValidMode(m string) bool {
	return m == "" || m == ModeInherit || m == ModeCustom || m == ModeOff
}

// Normalize validates a proxy URL and returns it in canonical form. An empty
// string is valid and means "no proxy".
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid proxy URL: expected scheme://[user:pass@]host:port")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if !allowedSchemes[u.Scheme] {
		return "", fmt.Errorf("unsupported proxy scheme %q (use http, https or socks5)", u.Scheme)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("proxy URL must include host and port")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("proxy URL must not contain a path, query or fragment")
	}
	u.Path = ""
	return u.String(), nil
}

// Mask hides the password of a proxy URL for API responses. Returns "" for an
// empty or unparseable value.
func Mask(plain string) string {
	if plain == "" {
		return ""
	}
	u, err := url.Parse(plain)
	if err != nil {
		return ""
	}
	if u.User == nil {
		return u.String()
	}
	if _, ok := u.User.Password(); !ok {
		return u.String()
	}
	// url.UserPassword would percent-encode the asterisks, so splice the
	// placeholder in by hand; the username is already escaped, so the first
	// "@" is the userinfo separator.
	u.User = url.User(u.User.Username())
	return strings.Replace(u.String(), "@", ":****@", 1)
}

// MaskEncrypted decrypts and masks a stored value.
func MaskEncrypted(encrypted string) string {
	return Mask(Decrypt(encrypted))
}

// Decrypt returns the plaintext of a stored proxy URL, or "" when unset or
// undecryptable.
func Decrypt(encrypted string) string {
	if encrypted == "" {
		return ""
	}
	plain, err := utils.Decrypt(encrypted)
	if err != nil {
		return ""
	}
	return plain
}

// PrepareUpdate turns a submitted proxy URL into the value to store. It
// returns changed=false when the submission is the masked form of the current
// value (the UI echoing back what it was shown), so edits that don't touch the
// field never clobber the stored password.
func PrepareUpdate(submitted, currentEncrypted string) (encrypted string, changed bool, err error) {
	current := Decrypt(currentEncrypted)
	if submitted != "" && submitted == Mask(current) {
		return currentEncrypted, false, nil
	}
	norm, err := Normalize(submitted)
	if err != nil {
		return "", false, err
	}
	if norm == current {
		return currentEncrypted, false, nil
	}
	if norm == "" {
		return "", true, nil
	}
	enc, err := utils.Encrypt(norm)
	if err != nil {
		return "", false, fmt.Errorf("failed to encrypt proxy URL")
	}
	return enc, true, nil
}

// Effective returns the plaintext proxy URL inst should use, or "" for none.
func Effective(inst database.Instance) string {
	switch inst.ProxyMode {
	case ModeOff:
		return ""
	case ModeCustom:
		return Decrypt(inst.ProxyURL)
	}
	raw, err := database.GetSetting(SettingKey)
	if err != nil {
		return ""
	}
	return Decrypt(raw)
}

// ApplyAgentEnv sets the conventional proxy env vars (upper- and lowercase,
// since tools disagree on which they read) on an agent container's env. A
// user-supplied NO_PROXY is kept and extended rather than replaced. No-op
// when proxyURL is empty, so users can still set these vars by hand.
func ApplyAgentEnv(env map[string]string, proxyURL string) {
	if proxyURL == "" {
		return
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		env[k] = proxyURL
		env[strings.ToLower(k)] = proxyURL
	}
	noProxy := mergeNoProxy(env["NO_PROXY"], env["no_proxy"])
	env["NO_PROXY"] = noProxy
	env["no_proxy"] = noProxy
	// Node's built-in fetch (undici) ignores the proxy env vars unless asked.
	env["NODE_USE_ENV_PROXY"] = "1"
}

func mergeNoProxy(existing ...string) string {
	seen := map[string]bool{}
	var out []string
	add := func(list string) {
		for _, h := range strings.Split(list, ",") {
			h = strings.TrimSpace(h)
			if h != "" && !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	add(NoProxy)
	for _, e := range existing {
		add(e)
	}
	return strings.Join(out, ",")
}
