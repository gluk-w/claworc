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

The image is covered by the vitest integration suite in `agent/tests/`
(`nanoclaw.test.ts`: toolchain and layout, the `CLAWORC_INITIAL_LLM_CONFIG`
boot path, the svc-agent supervisor, every shim verb, a chat turn through the
session-DB contract, and finally the image's own `shim-selftest`). It runs on
pull requests via `make agent-shim-test` and nightly via `make agent-test`:

```sh
make agent-shim-test            # builds claworc/hermes + claworc/nanoclaw, runs their suites
```

Or against an image you built yourself:

```sh
docker build -t claworc-nanoclaw:test agent/nanoclaw/
cd agent/tests && AGENT_NANOCLAW_TEST_IMAGE=claworc-nanoclaw:test npm run test -- nanoclaw.test.ts
```

The harness pins `--platform linux/amd64` to match CI; on Apple silicon set
`AGENT_TEST_PLATFORM=linux/arm64` (or build with `--platform linux/amd64`).

Unlike the daemonless template/Hermes images, the shim here needs its s6
services (the svc-agent supervisor) running, so the suite boots the container
and waits for `/opt/claworc/shim/health` to exit 0 before testing anything.

Chat checks need an Anthropic-compatible endpoint. Without one the agent
replies with the API error text and the turn still ends cleanly per contract;
the shim caps the Claude Code CLI's retry loop (`CLAUDE_CODE_MAX_RETRIES`,
default 3, override with `CLAWORC_NANOCLAW_MAX_RETRIES`) so that failure
takes seconds, not the CLI's default multi-minute backoff.
