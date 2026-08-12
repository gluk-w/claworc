# Claworc NanoClaw agent image

`claworc/nanoclaw` — a Claworc-managed agent image for
[NanoClaw](https://github.com/nanocoai/nanoclaw) (pinned via
`ARG NANOCLAW_VERSION`), implementing the
[Claworc Agent Shim Contract v1](../../docs/shim.md).

## How NanoClaw is run here (no Docker-in-Docker)

Upstream NanoClaw is two processes:

* a **host** (Node) that owns channels (WhatsApp/Telegram/…), routing, the
  central DB, the OneCLI credential gateway, and spawns **one Docker container
  per agent session** (`src/container-runtime.ts` hardcodes `docker`; the
  spawn hard-fails without the OneCLI gateway);
* an **agent-runner** (Bun + Claude Agent SDK) inside each session container.
  Host and runner communicate *only* through a per-session SQLite pair —
  `inbound.db` (host writes / runner reads) and `outbound.db` (runner writes /
  host reads: `messages_out`, `processing_ack`, `session_state`).

A Claworc instance container is already the sandbox, so this image does not
run the upstream host at all (there is no supported non-container executor
and the local `ncl` CLI needs the full host + Docker + OneCLI). Instead the
shim plays the host's role on the documented session-DB contract:

* `shim/lib/host.mjs` (svc-agent) supervises one **agent-runner child
  process** per Claworc session that has pending work, reaping idle ones;
* `chat-send` inserts the user message into the session's `inbound.db`,
  streams new `messages_out` chat rows as cumulative assistant snapshots, and
  ends the turn when the runner writes a terminal `processing_ack` for the
  message (a real marker — `chat_end_detection: "exact"`);
* `configure-llm` stores `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` routing
  (NanoClaw's native Claude SDK wiring) in a managed state file injected into
  every runner spawn, and writes the default model into NanoClaw's own
  `container.json`;
* one small build-time patch (`patches/0001-session-dir-env.patch`) makes the
  runner's fixed `/workspace` session mount point overridable via
  `NANOCLAW_SESSION_DIR` so several sessions can coexist as plain processes.
  The shared group dir stays at upstream's `/workspace/agent`, a symlink to
  the persistent `/home/claworc/workspace`.

Everything else is upstream-faithful: runner source is used unmodified from
the pinned checkout (`/app/src`, the layout its hook commands expect), deps
come from upstream's own `bun.lock`, and the Claude Code CLI is installed at
upstream's pinned version at `/pnpm/claude` (the path the runner hardcodes).

## Session and state model

| Path | Contents |
|---|---|
| `/home/claworc/workspace` | NanoClaw agent group dir: `container.json`, `CLAUDE.md`, `memory/`, `conversations/`, working files (PVC) |
| `/home/claworc/.claworc/shim/nanoclaw/sessions/<key>/` | per-Claworc-session `inbound.db` / `outbound.db` / `.heartbeat` / `outbox/` (PVC) |
| `/home/claworc/.claworc/shim/nanoclaw/llm.json` | managed configure-llm state (PVC) |
| `/run/claworc/shim/` | supervisor heartbeat + runner/chat pidfiles (ephemeral) |

`session-reset` kills the session's runner and deletes its session directory
(fresh SDK continuation). Long-term memory under the workspace is agent-level
state shared by all sessions — upstream semantics — and is not touched.

## Validate

The image ships the template's `shim-selftest` at
`/opt/claworc/shim/shim-selftest`. Unlike the daemonless template/Hermes
images it needs its s6 services (the svc-agent supervisor) running, so boot
the container first — this is exactly what `make agent-test` does:

```sh
docker build -t claworc/nanoclaw:test agent/nanoclaw/
docker run -d --name ncl-test claworc/nanoclaw:test
# wait until: docker exec ncl-test /opt/claworc/shim/health  → exit 0
docker exec ncl-test sh /opt/claworc/shim/shim-selftest /opt/claworc/shim
docker rm -f ncl-test
```

Chat checks need an Anthropic-compatible endpoint. Without one the agent
replies with the API error text and the turn still ends cleanly per contract;
the shim caps the Claude Code CLI's retry loop (`CLAUDE_CODE_MAX_RETRIES`,
default 3, override with `CLAWORC_NANOCLAW_MAX_RETRIES`) so that failure
takes seconds, not the CLI's default multi-minute backoff.
