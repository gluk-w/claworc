# Outbound Proxy

Routes an agent's outbound traffic through an HTTP, HTTPS or SOCKS5 proxy,
such as a residential proxy from ProxyEmpire. This covers both the
on-demand browser pod (Chromium) and the agent container's own HTTP calls
(curl, Python, Node tools, the agent runtime). Traffic to the LLM gateway and
other control-plane services stays direct.

Code: `control-plane/internal/outboundproxy/`.

## Configuration

A proxy is one URL: `scheme://[user:pass@]host:port`, where the scheme is
`http`, `https`, `socks5` or `socks5h`. Credentials containing reserved
characters must be percent-encoded (`@` → `%40`).

| Level     | Where                                         | Storage                                    |
|-----------|-----------------------------------------------|--------------------------------------------|
| Global    | Settings → Environment → Browser Defaults → Proxy | `default_proxy_url` setting (Fernet-encrypted) |
| Per agent | Agent page → Proxy (Default / Custom / Off)   | `instances.proxy_mode` + `instances.proxy_url` (encrypted) |

`proxy_mode` values:
- `inherit` (or empty), the default: use the global proxy, if one is set
- `custom`: use the agent's own `proxy_url`
- `off`: connect directly, even if a global proxy is set

The API only ever returns the URL with the password masked
(`http://user:****@host:port`). If a client sends that masked value back
unchanged, the stored URL is kept.

Changing the effective proxy restarts the affected running agents. Their
browser pods are also stopped, so the next browser open picks up the new
proxy. A global change only touches agents in `inherit` mode.

### ProxyEmpire example

```
http://<username>:<password>@<gateway-host>:<port>
```

Use the gateway host, port and username from the ProxyEmpire dashboard.
ProxyEmpire encodes country, rotation and sticky-session options in the
username, so you can give each agent its own sticky IP by setting a per-agent
Custom proxy with a different session suffix.

## Agent container

`applyReservedAgentEnv` (`handlers/instances.go`) calls
`outboundproxy.ApplyAgentEnv`, which sets the following when a proxy is in
effect:

- `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` and their lowercase forms, set to
  the full URL
- `NO_PROXY` / `no_proxy` = `localhost,127.0.0.1,::1,.svc,.cluster.local`,
  merged with any user-supplied `NO_PROXY`. Everything the control plane
  serves inside the container is on loopback through SSH tunnels: the LLM
  gateway and Composio broker on `127.0.0.1:40001`, CDP on `9222`, and the
  agent gateway. Those must stay direct.
- `NODE_USE_ENV_PROXY=1`, because Node's built-in `fetch` (undici) ignores the
  proxy vars without it

The vars reach s6 services through `with-contenv`, and SSH/terminal sessions
through `/etc/profile.d/claworc-env.sh`. They are not reserved names. When no
proxy is configured, users can still set them by hand as env vars, and a
configured proxy overrides those values.

Tools that ignore proxy env vars (raw sockets, some Go/Rust binaries with
custom transports) are not covered.

## Browser pod

Chromium's `--proxy-server` flag can't carry credentials, and Chromium has
no SOCKS5 auth at all. The browser image runs a local forwarder instead:

1. The bridge (`browserprov/bridge.go`) passes `CLAWORC_BROWSER_PROXY=<url>`
   into the pod.
2. `svc-proxy` (`agent/browser/rootfs/etc/s6-overlay/s6-rc.d/svc-proxy/run`)
   runs [gost](https://github.com/go-gost/gost) on `127.0.0.1:3128`. Gost
   forwards to the upstream proxy and handles authentication. Without the env
   var, the service idles.
3. `svc-desktop` starts Chromium with these flags:
   - `--proxy-server=http://127.0.0.1:3128`
   - `--force-webrtc-ip-handling-policy=disable_non_proxied_udp`, so WebRTC
     can't leak the pod IP

   Chromium bypasses loopback implicitly, so CDP and noVNC still work.

The credentials never appear in Chromium flags or in the browser profile.

Legacy combined agent+browser images (`IsLegacyEmbedded`) don't run
`svc-proxy`. Their Chromium is not proxied, but the agent env vars still apply.

## Verifying

- In the agent terminal, `curl https://api.ipify.org` should print the proxy's
  exit IP.
- In the browser, open `https://api.ipify.org`: you should see the same IP.
- Chatting with the agent should still work. If it doesn't, LLM traffic is
  going through the proxy; check `NO_PROXY`.
