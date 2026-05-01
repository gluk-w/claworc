# On-demand browser sessions (provider-pluggable, with K8s + Docker first)

## Context

Today every agent pod runs Chromium + Xvfb + TigerVNC + noVNC alongside OpenClaw, even when the user is not driving the browser. This wastes RAM and CPU on idle instances and ties the browser variant to the agent image. The user wants to:

- Move the browser out of the agent container into a separate, on-demand pod with no sshd of its own.
- Spawn that browser session lazily on the first CDP/VNC use, reuse the existing user profile if present, and reap the session after idle.
- Keep OpenClaw's CDP target unchanged (`http://127.0.0.1:9222`) — the plumbing under that endpoint is what moves.
- Design the abstraction so future providers (Cloudflare Browser Rendering, Browserless, BrowserBase, etc.) can be added behind the same interface; for now ship K8s + Docker.

User decisions captured:
- **Mode discriminator**: not a new column — instead, instances whose `Instance.ContainerImage` contains the substring `openclaw-vnc-` are legacy (combined-image, no separate browser pod). Anything else (e.g. `glukw/claworc-agent:latest`) is the new on-demand layout. A small helper `IsLegacyEmbedded(image string) bool` reads the image once at request time.
- **Legacy code retention**: the already-published `glukw/openclaw-vnc-{chromium,chrome,brave}:latest` images are frozen — existing instances keep pulling them from the registry. Their source files in `agent/` are deleted; this PR only ships the new image set.
- **Cold start**: bump `remoteCdpTimeoutMs` and `remoteCdpHandshakeTimeoutMs` to ~65 s in OpenClaw's `browser` config section so the first call can wait through provider spawn.
- **Isolation**: per-instance, 1:1 (strongest cookie/session isolation, simplest lifecycle).
- **Migration**: the instance-details page shows a banner for legacy-image instances. Clicking it runs a `TaskBrowserMigrate` task that (a) provisions the browser PVC and copies `chrome-data` over, (b) recreates the agent container/Deployment using the configured default agent image (`glukw/claworc-agent:latest` initially), (c) registers the CDP/VNC tunnels.
- **Settings-configurable timeouts and image defaults**: idle timeout, ready timeout, and the default agent image all live in the admin `settings` table, surfaced in the existing settings UI. Per-instance fields override globals where appropriate.
- **Spawn through TaskManager**: every browser session spawn (whether user click, first inbound CDP connection, or migration) runs as a `taskmanager` task with an `OnCancel` callback that rolls back partial state.
- **No sshd in the browser pod**: the browser pod runs only Chromium + Xvfb/TigerVNC/noVNC. CDP (9222) and noVNC (3000) are reached over cluster networking through a ClusterIP Service; isolation is enforced by NetworkPolicy. VNC streaming goes browser-pod → control plane directly, with no SSH detour. See §11.

## Architecture summary

A new `BrowserProvider` interface owns the lifecycle of a browser session (a Kubernetes pod, a Docker container, or — later — a Cloudflare/Browserless session). For every non-legacy instance, the control plane uses the existing `agent-listener` SSH tunnel pattern (already used for the LLM proxy on `127.0.0.1:40001`) so that `127.0.0.1:9222` is **bound on the agent by sshd**, not by Chromium. Inbound connections on that port are forwarded over the existing SSH session to the control plane, which calls into a `BrowserBridge` that (a) ensures a session exists via the configured provider, (b) waits for CDP readiness, (c) forwards bytes between the agent-side conn and the browser-pod CDP endpoint reached directly via cluster networking. VNC traffic is proxied straight from the browser pod's noVNC Service to the frontend WebSocket — the control plane does not run an SSH tunnel for it. A new `BrowserSession` table tracks lifecycle and `last_used_at`; an idle reaper goroutine spins down sessions after a configurable timeout.

## 1. Provider abstraction

New package `control-plane/internal/browserprov/`:

```go
type Provider interface {
    Name() string                                // "kubernetes" | "docker" | "cloudflare"
    Capabilities() Capabilities                  // SupportsVNC, SupportsPersistentProfile, SupportsHeadful

    EnsureSession(ctx context.Context, instanceID uint, params SessionParams) (*Session, error)
    StopSession(ctx context.Context, instanceID uint) error
    DeleteSession(ctx context.Context, instanceID uint) error // also wipes profile storage

    // CDP target. For K8s: returns net.Dial to the browser pod's ClusterIP Service on 9222.
    // For Docker: returns net.Dial to the browser container on the per-instance bridge.
    // For SaaS: returns an HTTP/WS-aware adapter that translates /json/* and per-target ws:// upgrades.
    DialCDP(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error)

    // Optional VNC stream. SaaS providers return ErrNotSupported. For K8s/Docker, the impl
    // returns a net.Dial to the browser pod's noVNC Service on port 3000.
    DialVNC(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error)

    SessionStatus(ctx context.Context, instanceID uint) (Status, error)
}
```

Initial implementations: `kubernetes.Provider` and `docker.Provider`. Cloudflare and similar SaaS providers slot in as separate sub-packages later. Per-instance selection: `Instance.BrowserProvider` (string, default per global setting).

## 2. Image strategy

Clean image lineup; legacy combined-image source is deleted from `agent/`.

New images, all under the `glukw/claworc-*` namespace:

- `glukw/claworc-agent:latest` — slim agent. s6 services: `init-setup`, `svc-sshd`, `svc-openclaw`, `svc-cron`. No Xvfb/VNC/openbox/browser.
- `glukw/claworc-browser-base:latest` — Xvfb, TigerVNC, noVNC, openbox, stealth-extension. **No sshd.** s6 services: `init-setup`, `svc-xvnc`, `svc-novnc`, `svc-desktop`. Container port `9222` (CDP) bound to `0.0.0.0` (cluster-reachable, NetworkPolicy-restricted); container ports `3000` (noVNC) and `5900` (raw VNC) similarly.
- `glukw/claworc-browser-chromium:latest` / `glukw/claworc-browser-chrome:latest` / `glukw/claworc-browser-brave:latest` — derive from `claworc-browser-base` and install the respective browser. The `svc-desktop` script keeps today's flags except `--remote-debugging-address` is removed (Chromium binds to `0.0.0.0:9222` so the cluster Service can reach it; access control is at the Service + NetworkPolicy layer).

Legacy `glukw/openclaw-vnc-{chromium,chrome,brave}:latest` images stay published in the registry; legacy instances keep pulling them. No source for those images remains in the repo.

Default agent image is configurable in admin settings (`default_agent_image`). Migration uses whatever value is set there.

## 3. Profile & PVC layout

Split PVCs:

- Agent home `<name>-home` keeps everything outside `chrome-data` (skills, workspace, dotfiles, **and `Downloads/`**).
- New `<name>-browser` PVC mounted at `/home/claworc/chrome-data` on the browser pod. Same in-pod path so `svc-desktop` flags don't change.
- Default size 10 Gi (`Instance.BrowserStorage`, mirrors `StorageHome`).

**Shared Downloads directory.** So that files Chromium saves are immediately visible to OpenClaw and the agent terminal, the browser pod *also* mounts the agent's `<name>-home` PVC with `subPath: Downloads` at `/home/claworc/Downloads`. Chromium is launched with `--download-directory=/home/claworc/Downloads`. No copy/sync — both pods write through the same volume.

This requires same-node placement (RWO PVC). The browser Deployment uses `requiredDuringSchedulingIgnoredDuringExecution` pod affinity targeting `app=<agent-name>`; if the agent's node is full or cordoned, the browser pod stays `Pending` rather than scheduling elsewhere where the mount would fail. Docker mode mounts the same per-instance home volume (`claworc-<name>-home`) into the browser container with `VolumeOptions.Subpath: "Downloads"`.

Legacy instances continue using a single `<name>-home` PVC where chrome-data lives inside it. The migration job (one-shot, idempotent) provisions `<name>-browser`, runs `cp -a /agent-home/chrome-data/. /browser-home/chrome-data/` and `rm -rf /agent-home/chrome-data`. Triggered by the migration banner; logged through existing audit logging. Providers without persistent profile storage (Cloudflare) skip the copy entirely.

## 4. CDP transport (the core mechanism)

Reuse the existing `TunnelTypeAgentListener` pattern.

**Agent change** — `agent/instance/rootfs/etc/ssh/sshd_config.d/claworc.conf`:
```
PermitListen 127.0.0.1:9222 127.0.0.1:40001
```

**OpenClaw `browser` config section** — bumped to:
- `remoteCdpTimeoutMs: 65000`
- `remoteCdpHandshakeTimeoutMs: 65000`
- `cdpUrl: "http://127.0.0.1:9222"` (unchanged)
- `attachOnly: true` (unchanged)

This is the existing browser section of OpenClaw's config that `svc-openclaw` already initialises at startup; no new file.

**Control-plane changes** — `control-plane/internal/sshproxy/tunnel.go`:
- Generalise `agentListenerLoop` to accept a `dial DialFunc` (`func(context.Context) (io.ReadWriteCloser, error)`). LLM-proxy callsites untouched.
- New constructor `CreateAgentListenerTunnelDial(ctx, instanceID, label, agentPort, dial)`.

New `control-plane/internal/browserprov/bridge.go`:
- `BrowserBridge.DialCDP(ctx, instanceID)`:
  1. `EnsureSession(ctx, instanceID)` — see §6; spawn (if needed) is run through TaskManager and `EnsureSession` blocks on its completion up to the configured ready timeout.
  2. Provider-specific readiness probe inside the spawn task: K8s/Docker poll `GET http://<browser-svc>:9222/json/version` (500 ms intervals); Cloudflare polls its session API.
  3. `provider.DialCDP(ctx, instanceID)` returns the upstream conn (for K8s: `net.Dial("tcp", <svc>:9222)`).
  4. Wrap in a `countingConn` that calls `bridge.Touch(instanceID)` on first byte, throttled to a 30 s in-memory flush.
- For non-byte-stream providers, `DialCDP` returns an HTTP/WS-aware adapter: parses agent HTTP requests on `/json/*`, synthesises CDP discovery responses, rewrites the per-target `ws://localhost:9222/devtools/page/<id>` upgrade onto the provider's session WebSocket. Out of scope for this PR; interface designed to allow it.

Tunnel registration during reconcile: for non-legacy instances, register a `CDP` agent-listener bound to `bridge.DialCDP`.

## 5. VNC routing

The existing `desktop.go` proxy is updated for non-legacy instances: instead of resolving a tunnel local port, it asks the bridge for a fresh dial to the browser pod's noVNC Service:

```go
conn, err := browserBridge.DialVNC(ctx, instanceID) // K8s: net.Dial to <svc>:3000
```

The handler then upgrades the incoming WebSocket and pumps bytes between it and `conn`. No SSH involved. Activity tracking (`bridge.Touch`) fires on each new connection.

For legacy instances, the existing reverse-tunnel path stays: agent:3000 → control-plane local port → `desktop.go`.

When the browser pod is reaped and a user opens the desktop tab, `DesktopProxy` first calls `bridge.EnsureSession(ctx, instanceID)`. New endpoint `GET /api/v1/instances/:id/browser/status` returns `{state, since, taskID?}` to power a "starting browser…" loading state in `useDesktop.ts`.

For providers without VNC (`Capabilities.SupportsVNC=false`), the desktop tab is hidden in the UI.

## 6. Browser session lifecycle

**DB** — new table `browser_sessions` (`internal/database/models.go`):

```go
type BrowserSession struct {
    InstanceID  uint   `gorm:"primaryKey"`
    Provider    string // "kubernetes" | "docker" | "cloudflare"
    Status      string // "stopped" | "starting" | "running" | "error"
    Image       string // for K8s/Docker; empty for SaaS providers
    PodName     string // K8s deployment / Docker container name; provider-defined for SaaS
    ProviderRef string // opaque provider-specific session ID
    LastUsedAt  time.Time
    StartedAt   time.Time
    StoppedAt   *time.Time
    ErrorMsg    string
    UpdatedAt   time.Time
}
```

**`Instance` model additions** (no `ProtoVersion` — discriminator is `ContainerImage`):
- `BrowserProvider string` — default from global settings, only consulted for non-legacy instances.
- `BrowserImage string` — for local providers (K8s/Docker).
- `BrowserIdleMinutes *int` — falls back to global setting.
- `BrowserStorage string` — PVC size, default `10Gi`.

A small helper in `internal/database/models.go`:

```go
func IsLegacyEmbedded(containerImage string) bool {
    return strings.Contains(containerImage, "openclaw-vnc-")
}
```

It's used at every code site that needs to choose between the legacy and new code paths (orchestrator, tunnel reconciler, desktop handler, instance handler, frontend response shaping).

**Orchestrator interface additions** — `internal/orchestrator/orchestrator.go` (used by K8s/Docker provider implementations):

```go
EnsureBrowserPod(ctx, instanceID, params) (BrowserEndpoint, error) // returns Service ClusterIP / DNS + ports
StopBrowserPod(ctx, instanceID) error
DeleteBrowserPod(ctx, instanceID) error
GetBrowserPodStatus(ctx, instanceID) (string, error)
GetBrowserPodEndpoint(ctx, instanceID) (cdpURL string, vncURL string, err error)
```

No `ConfigureBrowserPodSSH` — the browser pod doesn't run sshd.

**K8s impl** — new `internal/orchestrator/kubernetes_browser.go`. Deployment `<name>-browser` (1 replica, recreate strategy), PVC `<name>-browser`, ClusterIP Service exposing 9222 (CDP) and 3000 (noVNC) — `5900` not exposed since noVNC fronts it inside the pod. NetworkPolicy mirroring the agent's: ingress only from control-plane pods, only on those ports. `preferredDuringScheduling` pod affinity to co-locate with the agent for low latency.

**Docker impl** — new `internal/orchestrator/docker_browser.go`. Sibling container; for cross-instance isolation, each instance gets its own user-defined Docker bridge network (`claworc-net-<instance>`); the agent and browser containers join that bridge; the control plane attaches itself to each as needed (existing pattern for the claworc bridge generalised). Host-port mapping is not used — only the control plane reaches CDP/VNC, and it does so over the bridge by container DNS.

**SSH manager** — unchanged (no browser-side SSH). The agent-side SSH manager keying stays the same `uint` instanceID.

**TaskManager integration** — new task types added to `internal/taskmanager/taskmanager.go`:

```go
TaskBrowserSpawn   TaskType = "browser.spawn"
TaskBrowserMigrate TaskType = "browser.migrate"
```

`BrowserBridge.EnsureSession` is the single entry point, used by:
- the agent-listener loop on first CDP byte (`UserID=0`, system task — visible to admins),
- the desktop-tab pre-flight (`UserID=<viewer>`),
- the migration handler (`UserID=<initiator>`, type `TaskBrowserMigrate` instead).

`EnsureSession` semantics:
1. If `BrowserSession.Status='running'` and the CDP/VNC endpoints are reachable, return immediately.
2. Otherwise dedup against the in-memory `spawnInflight[instanceID]` map; if a spawn task is already running, wait on its completion channel.
3. Else `taskmanager.Start(StartOpts{Type: TaskBrowserSpawn, InstanceID, UserID, OnCancel: rollbackSpawn, Run: doSpawn})`. `Handle.UpdateMessage` reports phases ("Provisioning PVC", "Starting pod", "Waiting for CDP"). `Run` updates `BrowserSession.Status='starting'` then `'running'` on success or `'error'` on failure.
4. `OnCancel` rollback: cleanup callback calls `provider.StopSession`, deletes the partially-created Deployment/PVC if present, and sets `BrowserSession.Status='stopped'`.

The reaper from below runs as a background goroutine, not a TaskManager task. A per-instance mutex prevents reaper/spawn races.

**Idle reaper** — new goroutine in `bridge.go`, every 60 s:
- `SELECT instance_id FROM browser_sessions WHERE status='running' AND last_used_at < NOW() - timeout`.
- Per row: acquire per-instance lock, `provider.StopSession`, drop the CDP agent-listener tunnel, set `status='stopped'`.
- Activity tracking: `agentListenerLoop` (CDP) and the desktop handler (VNC) call `bridge.Touch(instanceID)` on each new connection; in-memory map flushed to DB every 30 s.

## 7. Settings & UI

**Admin-level settings** (existing `settings` table, surfaced in the settings page):
- `default_agent_image` — default `glukw/claworc-agent:latest`. Used for new instances and as the migration target.
- `default_browser_image` — default `glukw/claworc-browser-chromium:latest`.
- `default_browser_provider` — one of the registered providers (`kubernetes`, `docker`).
- `default_browser_idle_minutes` — integer, default `15`.
- `default_browser_ready_seconds` — integer, default `60`. Cap on how long `EnsureSession` waits before failing.
- Future: provider-specific credentials (Cloudflare account ID + API token, encrypted with the existing Fernet key).

**Per-instance fields** in the create/edit form:
- `Browser provider` — select.
- `Browser image` — for local providers.
- `Idle timeout (min)` — overrides `default_browser_idle_minutes` if set.

`InstanceDetailPage.tsx` shows a banner when `IsLegacyEmbedded(Instance.ContainerImage)`:

> This instance still runs the browser inside the agent container. **[Migrate to on-demand browser →]**

The button calls `POST /api/v1/instances/:id/browser/migrate`, which kicks off a `TaskBrowserMigrate` task that, in order:
1. Provisions the `<name>-browser` PVC.
2. Runs the chrome-data copy job (§3).
3. **Recreates the agent Deployment / container** using `default_agent_image` (e.g., `glukw/claworc-agent:latest`) and updates `Instance.ContainerImage` accordingly. Same name and PVC; image swap and rollout. After this step `IsLegacyEmbedded()` returns `false`.
4. Sets `Instance.BrowserProvider=<global default>` and `Instance.BrowserImage=<derived from old ContainerImage>` (e.g., `openclaw-vnc-chromium` → `glukw/claworc-browser-chromium:latest`).
5. Registers the CDP agent-listener tunnel; the browser session itself is left in `'stopped'` state (lazy spawn on first CDP/VNC use).

`OnCancel` for `TaskBrowserMigrate` deletes the partial browser PVC if step 1 had run, restores the agent's `chrome-data` if step 2 had partially run (the copy is staged into a temp directory and atomically swapped into place only if all phases succeed), and reverts `ContainerImage` if step 3 had run. Progress is surfaced as toasts via TaskManager's existing per-user broadcast.

## 8. Backward compat & migration

- Legacy: `IsLegacyEmbedded(Instance.ContainerImage)` true. Combined image still pulled from the registry. No browser session row, no CDP tunnel, VNC tunnel against the agent. Untouched.
- New: orchestrator builds the slim agent image and registers the browser provider; a `BrowserSession` row is created with `status='stopped'`; no provider session yet; the agent-listener tunnel for `127.0.0.1:9222` is registered and stays unbound until the first CDP connection wakes the bridge.
- Mode flip is one-way (legacy → new) through the migration banner. Going back is not supported and is documented.

## 9. Files to change / add / delete

**Agent (`/Users/stan/claworc/agent/`):**

Delete (legacy combined-image sources; published images stay in registry):
- `Dockerfile`
- `Dockerfile.chromium`
- `Dockerfile.chrome`
- `Dockerfile.brave`
- The combined s6 service set under `rootfs/etc/s6-overlay/s6-rc.d/user/contents.d/` that listed all services together.

Add:
- `agent/instance/Dockerfile` — slim agent (sshd, OpenClaw, cron). Uses the `agent.bundle` s6 set.
- `agent/browser/Dockerfile.base` — Xvfb/TigerVNC/noVNC/openbox/stealth-extension base. Uses the `browser.bundle` s6 set.
- `agent/browser/Dockerfile.chromium`, `agent/browser/Dockerfile.chrome`, `agent/browser/Dockerfile.brave`.
- New s6 bundles `agent.bundle` and `browser.bundle`.

Edit:
- `rootfs/etc/ssh/sshd_config.d/claworc.conf` — add `127.0.0.1:9222` to `PermitListen` (this stays in the agent image only; the browser image has no sshd).
- The OpenClaw `browser` config seed delivered by `svc-openclaw` — bump `remoteCdpTimeoutMs` and `remoteCdpHandshakeTimeoutMs` to `65000`. Same mechanism that exists today; no new file.
- Browser variant `svc-desktop` script — drop `--remote-debugging-address=127.0.0.1` so Chromium binds to all interfaces (cluster-internal only via Service + NetworkPolicy).

**Control plane (`/Users/stan/claworc/control-plane/`):**
- `internal/database/models.go` — add `BrowserSession`; add `Instance.BrowserProvider/BrowserImage/BrowserIdleMinutes/BrowserStorage`; add `IsLegacyEmbedded(image string) bool` helper.
- `internal/database/migrations/<ts>_browser_sessions.sql` (or gorm migrator equivalent) — create `browser_sessions`; add `Instance` columns. No `ProtoVersion`.
- `internal/browserprov/provider.go` (new) — `Provider` interface, `Capabilities`, `Session`, `SessionParams`.
- `internal/browserprov/bridge.go` (new) — `BrowserBridge` (DialCDP, DialVNC, Touch, EnsureSession, idle reaper, activity flusher).
- `internal/browserprov/kubernetes/provider.go` (new) — wraps `orchestrator.Kubernetes` browser-pod methods; resolves Service DNS for `DialCDP`/`DialVNC`.
- `internal/browserprov/docker/provider.go` (new) — wraps `orchestrator.Docker` browser-pod methods; per-instance bridge network; `DialCDP`/`DialVNC` use container DNS.
- `internal/orchestrator/orchestrator.go` — add browser-pod methods to interface (no SSH-related ones).
- `internal/orchestrator/kubernetes_browser.go` (new), `internal/orchestrator/docker_browser.go` (new). The K8s file builds Deployment + PVC + ClusterIP Service + NetworkPolicy. The Docker file creates a per-instance bridge network and joins both containers.
- `internal/sshproxy/tunnel.go` — generalise `agentListenerLoop` to accept a `DialFunc`; add `CreateAgentListenerTunnelDial`; reconciler registers the CDP tunnel for non-legacy instances.
- `internal/handlers/desktop.go` — for non-legacy instances, call `bridge.EnsureSession` then `bridge.DialVNC`; pump bytes onto the WebSocket. Legacy path unchanged.
- `internal/handlers/instances.go` — accept `browser_provider`, `browser_image`, `browser_idle_minutes` on create/update; new instances get `default_agent_image`; gate code paths on `IsLegacyEmbedded()`.
- `internal/handlers/browser.go` (new) — `GET /api/v1/instances/:id/browser/status`, `POST /browser/start`, `POST /browser/stop`, `POST /browser/migrate`. `start` and `migrate` return the spawned task ID for SSE subscription.
- `internal/taskmanager/taskmanager.go` — add `TaskBrowserSpawn` and `TaskBrowserMigrate` constants.
- `internal/handlers/settings.go` — accept and validate `default_agent_image`, `default_browser_image`, `default_browser_provider`, `default_browser_idle_minutes`, `default_browser_ready_seconds`.
- `main.go` (control-plane entry) — register providers, wire `BrowserBridge` (passing the task manager and settings reader), register routes.

**Frontend (`/Users/stan/claworc/control-plane/frontend/`):**
- `src/pages/InstanceDetailPage.tsx` — legacy-instance migration banner + button (shown when the response indicates `is_legacy_embedded`); subscribes to the returned `TaskBrowserMigrate` task ID via the existing tasks SSE stream for progress/cancel.
- `src/pages/InstancesPage.tsx` and the create/edit modal — provider/image/idle UI for non-legacy rows.
- `src/pages/SettingsPage.tsx` — admin fields for `default_agent_image`, `default_browser_image`, `default_browser_provider`, `default_browser_idle_minutes`, `default_browser_ready_seconds`.
- `src/hooks/useDesktop.ts` — handle 503 "starting" responses with retry/loading state, surfacing the running spawn task if one is found.
- `src/api/instances.ts` and `src/api/browser.ts` (new) — new endpoints.

**Helm (`/Users/stan/claworc/helm/`):**
- New templates for browser Deployment/PVC/Service/NetworkPolicy. Browser NetworkPolicy mirrors the agent's: ingress from control-plane pods only, on ports 9222 (CDP) and 3000 (noVNC).

## 10. Verification plan

**K8s e2e (`internal/orchestrator/kubernetes_browser_test.go` / e2e suite):**
1. Create instance via API. Assert `Instance.ContainerImage='glukw/claworc-agent:latest'`, `IsLegacyEmbedded()=false`, `BrowserSession.Status='stopped'`, no browser deployment yet, agent up, OpenClaw running.
2. Trigger an OpenClaw tool call needing CDP (e.g. `browser_navigate`). Assert: deployment+PVC+Service+NetworkPolicy appear, `/json/version` reachable from control plane via Service DNS, OpenClaw call returns 200, `BrowserSession.Status='running'`.
3. Connect `/api/v1/instances/:id/desktop/websockify`; assert frames flow without going through any SSH tunnel (validate by inspecting tunnel registry — no VNC reverse tunnel for this instance).
4. Set `default_browser_idle_minutes=2`, idle, observe reaper deletes deployment, `Status='stopped'`. Browser PVC retained.
5. Repeat the tool call — pod respawns, profile preserved (cookie set in step 2 still present).
6. Migration: simulate a legacy instance (image `glukw/openclaw-vnc-chromium:latest`), write data to `/home/claworc/chrome-data`, click the migration banner. Assert: chrome-data ends up on the browser PVC, agent Deployment now uses `glukw/claworc-agent:latest`, `IsLegacyEmbedded()=false`, `BrowserSession` row exists in `'stopped'` state.
7. Cross-instance isolation: from a sibling instance's pod, attempt `curl http://<browser-svc>:9222/json/version`. Assert connection refused / NetworkPolicy drop.
8. Delete instance — `DeleteBrowserPod` removes Deployment+PVC+Service+NetworkPolicy and the `BrowserSession` row.

**Docker variant (`docker_browser_test.go`):** same flow with sibling container on a per-instance bridge network. Cross-instance isolation: from a sibling instance's container, attempt to reach the browser container's CDP DNS name; assert it does not resolve / cannot connect.

**Manual checks:**
- `kubectl exec` into the control-plane pod and `curl http://<svc>:9222/json/version` — succeeds.
- `kubectl exec` into a sibling instance's agent and run the same — fails (NetworkPolicy).
- TaskManager toasts visible during spawn/migrate; cancel button rolls back partial state.

## 11. Network isolation — answer to "browser pod's CDP/VNC reachable from Claworc only?"

**Yes**, enforced by:

1. **Service exposure.** The browser pod's K8s `Service` exposes only ports 9222 (CDP) and 3000 (noVNC), as `ClusterIP` (not LoadBalancer/NodePort). No DNS name leaks outside the cluster.
2. **NetworkPolicy.** Mirrors `helm/templates/networkpolicy.yaml`: ingress to any pod with `managed-by=claworc` is allowed only from pods carrying the control plane's selector labels. Sibling agent and browser pods cannot talk to each other, and other tenants/workloads in the same namespace cannot reach 9222/3000 either. Egress from the browser pod is unrestricted (Chromium needs internet to render pages).
3. **Agent-side `PermitListen`.** The agent's sshd is the only thing that can advertise `127.0.0.1:9222` for OpenClaw to dial; that listener is created by the control plane's agent-listener tunnel and routes traffic back to the control plane, which then dials the browser pod's Service. No path inside the agent reaches the browser pod directly.

In Docker mode, equivalents are:
- A dedicated user-defined bridge network per instance (`claworc-net-<instance>`) so sibling instances cannot resolve or route to the browser container.
- The control plane joins each per-instance bridge as needed.
- No host-port mapping for 9222 or 3000.

CDP is not authenticated by Chromium itself — relying on cluster networking is therefore **not weaker than the previous SSH-tunneled design** because in either case anyone who could reach 9222 could control the browser. NetworkPolicy and per-instance Docker networks both make 9222 unreachable from anything except the control plane.

## 12. Risks / open items

- **HTTP/WS adapter for non-byte-stream providers** is the riskiest part of any future SaaS provider. The K8s/Docker path is byte-stream and trivial; the adapter is localized to its own provider impl. Non-blocking for this PR.
- **Docker per-instance bridge networks** — the existing Docker orchestrator uses a single shared bridge; switching to per-instance bridges is a small refactor but worth a careful pass to make sure existing integrations (LLM proxy, gateway port-forwarding) still work.
- **NetworkPolicy footprint** — relies on the cluster having a NetworkPolicy-enforcing CNI (e.g., Calico, Cilium). Helm chart README already documents this for the existing agent NetworkPolicy; reuse the same caveat.
- **Cold-start failure mode**: with `remoteCdpHandshakeTimeoutMs=65000`, a real CDP outage now surfaces as a ~65 s hang. Acceptable; revisit if user reports become noisy.
- **Activity accounting throttling** — 30 s flush window means the reaper can be up to 30 s late. Acceptable.
- **Migration rollback** — the agent Deployment image swap (step 3) is the trickiest reversal point because the rollout begins before the chrome-data copy is verified. The implementation stages the copy into a sibling directory and only flips the deployment image after the staging completes; rollback is then a simple image revert.
- **One-way migration** — flipping legacy → new is not reversible. Documented.
- **Helm rollout** — first release ships with `default_browser_provider=docker` for local dev and `kubernetes` for cluster installs; new images need to be published to the registry before merging.

## Critical files

- `/Users/stan/claworc/control-plane/internal/sshproxy/tunnel.go`
- `/Users/stan/claworc/control-plane/internal/orchestrator/kubernetes.go`
- `/Users/stan/claworc/control-plane/internal/orchestrator/docker.go`
- `/Users/stan/claworc/control-plane/internal/database/models.go`
- `/Users/stan/claworc/control-plane/internal/handlers/desktop.go`
- `/Users/stan/claworc/control-plane/internal/handlers/instances.go`
- `/Users/stan/claworc/control-plane/internal/taskmanager/taskmanager.go`
- `/Users/stan/claworc/agent/instance/rootfs/etc/ssh/sshd_config.d/claworc.conf`
- `/Users/stan/claworc/helm/templates/networkpolicy.yaml`
