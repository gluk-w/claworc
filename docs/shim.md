# Claworc Agent Shim Contract (v1)

The agent shim is the universal interface between the Claworc control plane and the AI
agent running inside an instance container. Any image that implements this contract can
be managed by Claworc — chat, webhooks, config editing, LLM virtual-key routing, and
health checks all go through the shim. OpenClaw, Hermes, NanoClaw, and custom images each
ship their own shim implementation.

The contract is **exec-based**: the control plane invokes well-known executables inside
the container over the instance's SSH connection. There is no required daemon, port, or
wire protocol beyond SSH (which every Claworc image already runs). Most verbs are
implementable as small shell scripts; images are free to use any language.

Layering (control-plane side):

```
handlers / frontend
   └── internal/agentshim   (Client/Session interfaces + adapters — all agent knowledge)
          └── internal/sshproxy    (SSH exec / SFTP / tunnels — transport only)
                 └── internal/orchestrator  (container lifecycle only — no agent knowledge)
```

## Image layout

```
/opt/claworc/shim/
├── agent.txt        # single-line agent display name, e.g. "OpenClaw"
├── agent.svg        # square logo, shown in the instance list and detail header
├── meta             # executable verbs (any language, shebang OK, mode 0755)
├── health
├── chat-send
├── chat-abort
├── session-reset
├── config-get
├── config-set
├── configure-llm
├── restart
└── lib/             # optional shared helpers, not invoked by the control plane
```

- Verbs are invoked **as root** (SSH is the authentication boundary, exactly like the
  terminal and file browser). Shims MUST drop to the `claworc` user for anything that
  touches agent state (`s6-setuidgid claworc`, `su - claworc -c`, …) so files stay
  claworc-owned.
- `agent.txt` and `agent.svg` are static files read over the SSH channel — they must
  not require the agent to be running.
- Shim persistent state (session maps, transcripts) lives in
  `/home/claworc/.claworc/shim/` (on the instance PVC, survives restarts). Ephemeral
  runtime state (PIDs) lives in `/run/claworc/shim/`.

## Common conventions for all verbs

- Structured output is UTF-8 JSON on **stdout**. Human-readable diagnostics go to
  **stderr** (surfaced in control-plane error messages).
- **Exit codes**:

  | Code | Meaning |
  |------|---------|
  | 0 | success |
  | 1 | internal failure |
  | 2 | usage error (bad arguments) |
  | 3 | verb/capability unsupported by this agent |
  | 4 | agent not ready (still booting) |
  | 5 | timed out waiting on the agent |
  | 6 | validation failed (bad config / payload); stdout carries `{"error":"..."}` |

- `configure-llm`, `config-set`, `session-reset`, and `restart` MUST be idempotent.
- Consumers MUST ignore unknown JSON fields and unknown event types (forward
  compatibility within contract v1). Breaking changes bump the `contract` integer.

## Verbs

### `meta`

Capability and version probe. No arguments, no stdin. Prints one JSON object:

```json
{
  "contract": 1,
  "shim_version": "0.1.0",
  "agent": {"name": "openclaw", "version": "2.7.1"},
  "capabilities": ["chat", "chat.abort", "session.reset", "config", "configure-llm", "restart", "control-ui"],
  "config_files": [
    {"id": "main", "path": "/home/claworc/.openclaw/openclaw.json",
     "language": "json", "label": "openclaw.json", "restart_required": true}
  ],
  "workspace_dir": "/home/claworc/.openclaw/workspace",
  "skills_dir": "/home/claworc/.openclaw/skills",
  "log_files": [{"path": "/var/log/claworc/agent.log", "label": "Agent"}],
  "llm": {"styles": ["openai"]},
  "session_persistence": "native"
}
```

Field notes:

- `contract` (required): integer contract version this shim implements. The control
  plane rejects versions outside its supported range.
- `capabilities` (required): gates features in the UI. `chat` is **required** — images
  without it fail validation. Optional: `chat.abort`, `session.reset`, `config`,
  `configure-llm`, `restart`, `control-ui` (agent serves its own web UI that Claworc
  reverse-proxies), `skills` (supports Claworc skills sync into `skills_dir`).
- `config_files`: files exposed in the Config tab. `language` drives editor syntax
  highlighting (`json`, `yaml`, `toml`, `ini`, `shell`, `plaintext`). Empty array (or
  no `config` capability) hides the Config tab. `restart_required: true` makes the
  control plane call `restart` after `config-set`.
- `llm.styles`: which API dialect(s) the agent will use when calling the LLM proxy —
  `openai` and/or `anthropic`. The control plane verifies its gateway supports the
  declared style.
- `skills_dir`: directory Claworc syncs skills into (only meaningful with the
  `skills` capability).
- `session_persistence`: `native` (agent resumes sessions by key), `emulated` (shim
  replays transcripts), or `none` (each turn is fresh). Optional; consumers treat an
  absent field as `native`.
- `chat_end_detection` (optional): `exact` (default) or `heuristic` — declare
  `heuristic` when end-of-turn is inferred (e.g. quiet-period detection), so the UI can
  soften "done" indicators.

### `health`

No arguments. Exit `0` when the agent can take a chat turn, `4` while booting, `1` when
broken. Optionally prints `{"status":"ok","detail":"..."}`.

### `chat-send --session <key> [--turn <id>]`

The core verb. Sends one user message to the agent and streams the agent's response.

- **stdin**: the raw UTF-8 user message, read until EOF. Attachments are not in-band —
  the control plane uploads files via SFTP beforehand and references them in the message
  text.
- **stdout**: JSONL — one event object per line (schema below), terminated by exactly
  one `end` event, then exit `0`.
- `--session <key>`: opaque Claworc-chosen session key (e.g. `browser`,
  `claworc-webhook-<name>`). The shim maps it to agent-native sessions and MUST
  preserve conversation history across turns for the same key (unless
  `session_persistence` is `none`).
- `--turn <id>`: optional caller-supplied turn id echoed in events; the shim generates
  one when absent.
- **Abort semantics**: the primary abort path is the `chat-abort` verb — the running
  `chat-send` then emits `end` with `"stop_reason":"aborted"` and exits 0. The shim
  SHOULD also handle SIGTERM/HUP the same way (note: many sshds do not deliver signal
  requests; SSH channel teardown is the delivery mechanism the control plane relies
  on, so shims must tolerate being killed without emitting `end`). History up to the
  abort stays in the session.
- Exit `0` iff an `end` event was emitted (including aborted/error ends). A non-zero
  exit means the shim/transport itself failed; the control plane surfaces the last
  `error` event or a stderr tail.

#### Chat event schema (JSONL)

```jsonl
{"v":1,"event":"start","session":"browser","turn":"t-9f2c"}
{"v":1,"event":"assistant","turn":"t-9f2c","message_id":"m1","text":"Looking into it"}
{"v":1,"event":"assistant","turn":"t-9f2c","message_id":"m1","text":"Looking into it now. I'll check the file."}
{"v":1,"event":"tool","turn":"t-9f2c","name":"exec","phase":"start","detail":{"command":"ls /tmp"}}
{"v":1,"event":"tool","turn":"t-9f2c","name":"exec","phase":"result","detail":{"exit":0}}
{"v":1,"event":"assistant","turn":"t-9f2c","message_id":"m2","text":"Done. Two files found."}
{"v":1,"event":"error","turn":"t-9f2c","code":"provider_rate_limit","text":"rate limited","fatal":false}
{"v":1,"event":"end","turn":"t-9f2c","stop_reason":"complete","text":"Done. Two files found."}
```

Rules:

- **`assistant.text` is a CUMULATIVE SNAPSHOT** of the message identified by
  `message_id` — the full text so far, not a delta. Snapshots are self-healing over a
  buffered pipe: a dropped or coalesced line costs latency, never correctness. Agents
  that natively stream deltas accumulate them in the shim (two lines of code); the
  reverse (snapshot→delta) would require diffing. A turn may contain multiple
  `message_id`s (text → tool calls → more text); each snapshot replaces only its own
  message.
- Shims SHOULD throttle snapshots (≥150 ms apart, or on message boundaries) to bound
  output size on long responses.
- `end` is required, exactly once, last. `stop_reason` ∈ `complete | aborted | error`.
  `end.text` carries the final text of the last assistant message so one-shot consumers
  (webhooks) can ignore everything else. Consumers stop reading at `end` and discard
  any output after it.
- `tool` events are optional; `detail` is free-form JSON.
- `error` with `"fatal":false` is informational; a fatal error should be followed by
  `end` with `stop_reason:"error"`.
- Unknown event types MUST be ignored by consumers.

The control plane forwards these events (verbatim JSON) to the browser chat UI over its
WebSocket, prefixed by a `{"type":"connected"}` handshake frame — the shim event schema
is also the browser chat protocol.

### `chat-abort --session <key>`

Aborts the in-flight turn for the session (the running `chat-send` emits `end/aborted`
and exits). Exit `0` also when nothing was running.

### `session-reset --session <key>`

Clears conversation history for the key; the next `chat-send` starts fresh. Backs the
`/new` and `/reset` chat commands.

### `config-get [--id <file-id>]` / `config-set [--id <file-id>]`

Raw config file bytes on stdout (`config-get`) / stdin (`config-set`). `--id` selects an
entry from `meta.config_files` (defaults to the first). `config-set` MAY validate and
exit `6` with `{"error":"..."}` on stdout; it MUST NOT restart the agent — the control
plane calls `restart` afterwards when the file declares `restart_required`.

### `configure-llm`

Routes all of the agent's LLM traffic through the Claworc LLM proxy using virtual keys.
stdin is the generic routing document:

```json
{
  "proxy_url": "http://127.0.0.1:40001",
  "style": "openai",
  "default_model": "anthropic/claude-sonnet-4-5",
  "fallback_models": [],
  "providers": [
    {"key": "anthropic", "api_key": "claworc-vk-abc123", "api_type": "anthropic-messages",
     "models": [{"id": "anthropic/claude-sonnet-4-5"}]}
  ]
}
```

`providers[].api_type` is optional metadata for dialect-aware shims (e.g. OpenClaw's
provider `api` field); shims MUST ignore fields they don't understand. Model entries
carry only `id`; the default model is `default_model`, never a per-model flag.

The shim rewrites the agent's native model/provider configuration so requests go to
`proxy_url` authenticated by the virtual key(s). MUST be idempotent — rewrite a fully
managed section, never append. Exit `6` if the routing cannot be expressed.

This verb is also invoked **by the image itself at boot**: when the
`CLAWORC_INITIAL_LLM_CONFIG` environment variable is set, the image's startup script
pipes its value into its own `configure-llm` before starting the agent service.

### `restart`

Restarts the agent service (`s6-svc -r /run/service/svc-agent` or equivalent). Exit `0`
when the restart was accepted; a no-op exit `0` is fine for agents with no daemon.

## Environment variables (set by the control plane)

| Variable | Purpose |
|---|---|
| `CLAWORC_INSTANCE_ID` | Instance identifier (existing) |
| `CLAWORC_AGENT_TOKEN` | Secret for intra-container agent auth (e.g. OpenClaw maps it to its gateway token) |
| `CLAWORC_INITIAL_LLM_CONFIG` | `configure-llm` JSON document applied at first boot |
| `CLAWORC_LLM_PROXY_URL` | LLM proxy URL, normally `http://127.0.0.1:40001` |

These names are reserved (users cannot override them). For OpenClaw images the legacy
`OPENCLAW_GATEWAY_TOKEN`, `OPENCLAW_INITIAL_MODELS`, and `OPENCLAW_INITIAL_PROVIDERS`
variables remain reserved and are still injected for backward compatibility.

## Service & filesystem conventions

- s6-overlay services: `svc-sshd` (required — the contract's only hard runtime
  dependency), `svc-agent` (the agent daemon, if any), `init-agent-seed` (oneshot
  first-boot seeding of `/home/claworc` from a baked skeleton).
- Primary agent log at `/var/log/claworc/agent.log` (declared in `meta.log_files`;
  Claworc's log streaming tails `/var/log/claworc/`).
- Persistent agent state under `/home/claworc` (the instance PVC).

## Probe, validation, and degraded mode

On every SSH (re)connect — and after image updates — the control plane:

1. reads `/opt/claworc/shim/agent.txt` and `agent.svg` over the SSH channel,
2. runs `/opt/claworc/shim/meta` with a short timeout.

Outcomes:

- **shim mode** — meta parses, `contract` supported, `chat` capability present. The
  identity and meta document are cached on the instance record.
- **legacy-openclaw** — no shim, but the `openclaw` CLI exists: the control plane falls
  back to its built-in native OpenClaw adapter (pre-shim images keep working).
- **shim-missing / shim-incompatible** — chat, config, and webhooks are disabled with
  an explanatory banner; terminal, file browser, logs, and VNC remain fully functional.

## Minimal shell reference implementation

A bare-bones custom image can implement chat with a wrapper around any CLI agent
(`CHAT_CMD` reads the message on stdin and writes the reply to stdout):

```sh
#!/bin/sh
# /opt/claworc/shim/chat-send — minimal single-snapshot implementation
set -eu
SESSION=""; TURN=""
while [ $# -gt 0 ]; do
  case "$1" in
    --session) SESSION="$2"; shift 2 ;;
    --turn)    TURN="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$SESSION" ] || { echo "--session is required" >&2; exit 2; }
[ -n "$TURN" ] || TURN="t-$$"

json_escape() { python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))'; }

printf '{"v":1,"event":"start","session":%s,"turn":%s}\n' \
  "$(printf %s "$SESSION" | json_escape)" "$(printf %s "$TURN" | json_escape)"

REPLY=$(su - claworc -c "$CHAT_CMD" 2>/dev/null) || {
  printf '{"v":1,"event":"end","turn":%s,"stop_reason":"error","text":""}\n' \
    "$(printf %s "$TURN" | json_escape)"
  exit 0
}
T=$(printf %s "$REPLY" | json_escape)
TU=$(printf %s "$TURN" | json_escape)
printf '{"v":1,"event":"assistant","turn":%s,"message_id":"m1","text":%s}\n' "$TU" "$T"
printf '{"v":1,"event":"end","turn":%s,"stop_reason":"complete","text":%s}\n' "$TU" "$T"
```

`meta`, `health`, `config-get`/`config-set`, and `restart` are each a few lines of
shell (`cat` a JSON heredoc, `pgrep`, `cat`/`tee` the config path, `s6-svc -r`). The
`agent/template/` directory ships a complete copy-me implementation plus
`shim-selftest`, a conformance script that exercises every verb and validates the JSONL
output; run it in CI for every agent image.
