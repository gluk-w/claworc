# Claworc Agent Shim Contract (v1)

The agent shim is the universal interface between the Claworc control plane and the AI
agent running inside an instance container. Any image that implements this contract can
be managed by Claworc — chat, webhooks, config editing, LLM virtual-key routing, and
health checks all go through the shim. OpenClaw, Hermes, NanoClaw, and custom images each
ship their own shim implementation.

The contract is **exec-based**: the control plane invokes well-known executables inside
the container over the instance's SSH connection. There is no required daemon, port, or
wire protocol beyond SSH (which every Claworc image already runs). Every verb is a
small bash entrypoint; logic that needs real parsing or a protocol client lives in
`lib/`, written in whatever runtime the image already ships (see
[Implementing a shim](#implementing-a-shim)).

To package a new agent end to end (image, tests, CI, registry entry), see
[adding-an-agent.md](adding-an-agent.md).

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
├── meta             # verb entrypoints: bash scripts, mode 0755
├── health
├── chat-send
├── chat-stream
├── chat-abort
├── session-reset
├── config-get
├── config-set
├── configure-llm
├── restart
├── skill-install
├── skill-remove
├── control-ui-auth  # only with the control-ui capability
├── shim-selftest    # conformance script (not invoked by the control plane)
└── lib/             # not invoked by the control plane
    ├── shimlib.sh   # shared bash plumbing, sourced by every verb (mode 0644)
    └── …            # runtime implementations the verbs exec (node/bun/python)
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

## Implementing a shim

### Entrypoints are bash

Every verb in `/opt/claworc/shim/` is a bash script that starts the same way:

```bash
#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/lib/shimlib.sh"
```

One entry language keeps every image's shim readable side by side, and the contract
exit codes come straight from the verb's process status. Nothing sits between the
control plane and the verb, so there's no `make` or other dispatcher that would
flatten exits 2/3/4/6 into its own failure code.

### Runtime logic lives in `lib/`

Plumbing — argument parsing, exit codes, atomic writes, skills — is bash in
`lib/shimlib.sh`. When a verb needs real parsing or a protocol client (a streaming
chat bridge, JSON routing documents, SQLite), its entrypoint `exec`s into
`lib/<verb>.<ext>`, using a runtime the image **already ships** for the agent itself.
Stdin, stdout, stderr, and the exit status pass straight through, and `exec` keeps the
PID (chat-abort relies on it). Never install a runtime just for the shim.

```bash
#!/usr/bin/env bash
# configure-llm — routing document on stdin, parsed by lib/configure-llm.py
set -euo pipefail
source "$(dirname "$0")/lib/shimlib.sh"

parse_no_args "$@"
exec python3 "$SHIM_DIR/lib/configure-llm.py"
```

Verbs that need nothing but files stay pure bash. A complete `config-set`:

```bash
#!/usr/bin/env bash
# config-set [--id <file-id>] — replace the config file with stdin, byte for byte.
set -euo pipefail
source "$(dirname "$0")/lib/shimlib.sh"

parse_id_arg main main -- "$@"
atomic_write /home/claworc/.myagent/config.json 0644
```

What the shipped images do:

| Verb | OpenClaw | Hermes | NanoClaw | Template |
|---|---|---|---|---|
| `meta`, `health`, `restart` | bash | bash | bash | bash |
| `config-get`, `config-set` | bash | bash | bash | bash |
| `skill-install`, `skill-remove` | bash | bash | bash | bash |
| `configure-llm` | → `lib/configure-llm.cjs` (Node) | → `lib/configure-llm.py` | → `lib/configure-llm.mjs` (Bun) | → `lib/configure-llm.py` |
| `chat-send` | → `lib/gateway-bridge.mjs` (Node) | → `lib/chat-send.py` | → `lib/chat-send.mjs` (Bun) | bash (`CHAT_CMD` wrapper) |
| `chat-stream` | → `lib/gateway-bridge.mjs stream` (Node, one gateway WebSocket) | bash (`chat_stream_loop`) | bash (`chat_stream_loop`) | bash (`chat_stream_loop`) |
| `control-ui-auth` | bash + inline Node (reads the gateway token) | — | — | — |
| `chat-abort`, `session-reset` | → `lib/gateway-bridge.mjs` (Node) | bash | → `lib/*.mjs` (Bun) | bash |

OpenClaw's `gateway-bridge.mjs` connects to the local gateway as a backend client
(`gateway-client`, mode `backend`, no Origin header), using the gateway token and its own Ed25519
device identity. The key lives in `/home/claworc/.claworc/shim/gateway-device.json`
(claworc-owned, `0600`). The bridge signs the gateway's `connect.challenge` nonce with the v3
device-auth payload, and the gateway pairs loopback devices silently on first connect.
OpenClaw 2026.9+ rejects device-less connections that claim to be the Control UI, and it ties
that client id to a browser Origin and a matching UI build, so the bridge must not use it.

### `lib/shimlib.sh`

The copy in `agent/template/shim/lib/shimlib.sh` is canonical. Every image ships a
byte-identical copy, which `agent/tests/shim-contract.ts` enforces, so fix bugs there
and copy the file out. Keep it agent-agnostic: paths and runtimes belong in the verbs.

| Helper | Behavior |
|---|---|
| `SHIM_DIR` | Absolute path of the shim directory (set on source). |
| `die_usage <msg>` | Message to stderr, exit 2. |
| `die_unsupported <msg>` | Message to stderr, exit 3. |
| `die_validation <msg>` | `{"error":"<msg>"}` (JSON-escaped) on stdout, exit 6. |
| `json_string <s>` | Print `<s>` as a JSON string literal. |
| `parse_id_arg <default> <ids…> -- "$@"` | Parse `[--id <id>]` into `SHIM_ID`; unknown args or ids exit 2. |
| `parse_name_arg "$@"` | Parse required `--name <name>` into `SHIM_NAME`; must match `[A-Za-z0-9._-]+`, not `.`/`..` (else exit 2). |
| `parse_session_args "$@"` | Parse `--session <key> [--turn <id>]` into `SHIM_SESSION`, `SHIM_TURN`, and `SHIM_SESSION_SLUG` (file-name-safe key); a missing `--session` exits 2. |
| `parse_no_args "$@"` | Exit 2 if any argument is given. |
| `run_as_claworc <cmd…>` | `exec` the command as `claworc` (`s6-setuidgid`, `HOME=/home/claworc`) when running as root; plain `exec` otherwise. |
| `chown_claworc <path…>` | `chown claworc:claworc` when running as root; no-op otherwise. |
| `mkdir_claworc <dir>` | `mkdir -p` that gives every directory it creates to `claworc`. |
| `atomic_write <path> [mode]` | Stdin → temp file in the same directory → mode (default `0644`) + claworc owner → `mv -f` over `<path>`. |
| `cat_file <path>` | `cat` the file, or exit 1 if it doesn't exist. |
| `skill_install <skills_dir> <name>` | The `skill-install` behavior below. |
| `skill_remove <skills_dir> <name>` | `rm -rf <skills_dir>/<name>`. |
| `chat_stream_loop` | The whole `chat-stream` verb, built on the image's own `chat-send` / `chat-abort` / `session-reset` (call after `parse_session_args`). |

### Checking an image

`agent/template/shim/shim-selftest` (bash + `jq` + `tar`) exercises every verb inside a
running container. Copy it into your image and run it in CI:
`docker run --rm my-agent /opt/claworc/shim/shim-selftest`. The integration suites
under `agent/tests/` (`shim-contract.ts` plus one `<agent>.test.ts` per image) are the
authoritative checks for the shipped images.

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

- `configure-llm`, `config-set`, `session-reset`, `skill-install`, `skill-remove`, and
  `restart` MUST be idempotent.
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
  "capabilities": ["chat", "chat.stream", "chat.abort", "session.reset", "config", "configure-llm", "restart", "control-ui"],
  "config_files": [
    {"id": "main", "path": "/home/claworc/.openclaw/openclaw.json",
     "language": "json", "label": "openclaw.json", "restart_required": true}
  ],
  "workspace_dir": "/home/claworc/.openclaw/workspace",
  "skills_dir": "/home/claworc/.openclaw/skills",
  "log_files": [{"path": "/var/log/claworc/agent.log", "label": "Agent"}],
  "llm": {"styles": ["openai"]},
  "session_persistence": "native",
  "control_ui": {"port": 18789, "base_path": "/openclaw/42/"}
}
```

Field notes:

- `contract` (required): integer contract version this shim implements. The control
  plane rejects versions outside its supported range.
- `capabilities` (required): gates features in the UI. `chat` is **required** — images
  without it fail validation. Optional: `chat.stream` (implements `chat-stream`),
  `chat.abort`, `session.reset`, `config`, `configure-llm`, `restart`, `control-ui`
  (agent serves its own web UI that Claworc reverse-proxies; see `control_ui`),
  `skills` (implements `skill-install` / `skill-remove`).
- `config_files`: files exposed in the Config tab. `language` (`json`, `yaml`, `toml`,
  `ini`, `shell`, `plaintext`) drives editor syntax highlighting and is the frontend's
  validation hint: `json` and `yaml` are parsed in the browser before `config-set` runs. Empty array (or
  no `config` capability) hides the Config tab. `restart_required: true` makes the
  control plane call `restart` after `config-set`.
- `llm.styles`: which API dialect(s) the agent will use when calling the LLM proxy —
  `openai` and/or `anthropic`. The control plane verifies its gateway supports the
  declared style.
- `skills_dir`: where the agent loads skills from and where `skill-install` unpacks
  them (`<skills_dir>/<skill-name>/`). Informational for the control plane — it only
  talks to the verbs. The control plane does not restart the agent after a skill
  change.
- `session_persistence`: `native` (agent resumes sessions by key), `emulated` (shim
  replays transcripts), or `none` (each turn is fresh). Optional; consumers treat an
  absent field as `native`.
- `control_ui` (required with `control-ui`): `port` is the container-local TCP port
  the UI listens on, `base_path` the path prefix it is served under. The control plane
  tunnels to that port and proxies `/openclaw/<instance-id>/…` to it; authentication
  comes from the `control-ui-auth` verb.
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

### `chat-stream --session <key>`

Optional (`chat.stream` capability). A persistent chat channel: one long-lived exec per
chat session instead of one `chat-send` per turn. The control plane prefers it when
declared and falls back to per-turn `chat-send` otherwise. It is what lets turns the
agent starts on its own (cron jobs, heartbeats) reach an open chat.

**stdin**: one command per line, simple enough to parse with bash `read`:

| Command | Meaning |
|---|---|
| `send <turn-id> <base64-message>` | Queue a user message (UTF-8, base64 without line breaks; may be empty). |
| `abort` | Abort the in-flight turn now (as `chat-abort`); queued commands stay queued. |
| `reset` | Queue a history reset (as `session-reset`), run after the in-flight turn. |

Unknown commands are ignored (a note goes to stderr).

**stdout**: first `{"v":1,"event":"ready"}` once the channel works, then the chat event
schema below. Rules:

- **Single writer**: every line comes from one writer, so lines never interleave.
- **Serialized**: sends and resets run one at a time, in order. Each own turn uses the
  given turn id and ends with exactly one `end`.
- **Unsolicited turns**: a turn the agent starts for this session key without a `send`
  begins with `start` carrying a shim-picked turn id, and ends with `end` as usual.
- A failed `reset` is reported as `{"event":"error","code":"reset_failed","fatal":false}`
  (no turn); a missing session is not a failure.

**Lifecycle**: stdin EOF drops queued commands, aborts the in-flight turn (its `end`
with `stop_reason: "aborted"` is still printed) and exits `0`. SIGTERM/HUP do the same.
Exit `4` when the agent isn't reachable at startup; exit `1` when the channel to the
agent breaks. If the process dies mid-turn, the control plane reports an error `end`
for that turn and reopens the stream on the next message.

Images that don't need a native long-lived connection implement the verb with
shimlib's generic loop:

```bash
#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/lib/shimlib.sh"

parse_session_args "$@"
chat_stream_loop
```

### `chat-abort --session <key>`

Aborts the in-flight turn for the session (the running `chat-send` emits `end/aborted`
and exits). Exit `0` also when nothing was running.

### `session-reset --session <key>`

Clears conversation history for the key; the next `chat-send` starts fresh. Backs the
`/new` and `/reset` chat commands.

### `config-get [--id <file-id>]` / `config-set [--id <file-id>]`

Raw config file bytes on stdout (`config-get`) / stdin (`config-set`). `--id` selects an
entry from `meta.config_files` (defaults to the first); an unknown id exits `2`.
Both verbs pass bytes through **unchanged** — no parsing, no validation, no
reformatting. `config-set` writes atomically (temp file + rename, claworc-owned).
Validation is the frontend's job, driven by the file's `language`. `config-set` MUST NOT
restart the agent — the control plane calls `restart` afterwards when the file declares
`restart_required`.

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
`proxy_url` authenticated by the virtual key(s).

`default_model` and model ids are `<provider-key>/<model>`. The proxy forwards the
request path and model unchanged to the provider the virtual key belongs to, so
shims whose agent speaks the plain OpenAI client dialect (a single base URL + key,
like Hermes' `custom` provider) must:

- use the virtual key of the provider named by the `default_model` prefix (fall back
  to the first provider), not blindly `providers[0]`;
- strip the `<provider-key>/` prefix from the model id;
- point the client at `proxy_url` + `/v1` (the client appends `/chat/completions`).

Getting any of these wrong makes every LLM call 404 upstream. MUST be idempotent — rewrite a fully
managed section, never append. Exit `6` if the routing cannot be expressed.

This verb is also invoked **by the image itself at boot**: when the
`CLAWORC_INITIAL_LLM_CONFIG` environment variable is set, the image's startup script
pipes its value into its own `configure-llm` before starting the agent service.

### `skill-install --name <skill>`

Installs or replaces one skill. stdin is an uncompressed tar archive of the skill's
files, with paths relative to the skill root (`SKILL.md`, `scripts/run.sh`, …).
The verb:

1. rejects the archive with exit `6` and `{"error":"..."}` if any entry is an absolute
   path, contains a `..` component, or is anything other than a regular file or
   directory (symlinks, hard links, devices, FIFOs);
2. unpacks into a staging directory next to the target, ignoring archived owners and
   setuid/setgid bits, and hands everything to `claworc`;
3. swaps the staged directory in for `<skills_dir>/<skill>` — a **full replace**, so
   files dropped from the skill disappear.

Nothing is written when validation fails, and the installed version stays as it was.
`--name` must match `[A-Za-z0-9._-]+` and must not be `.` or `..`; anything else exits
`2`. Images without the `skills` capability don't ship the skill verbs, and the
control plane never calls them; if you ship them anyway, make them `die_unsupported`
(exit `3`).

### `skill-remove --name <skill>`

Deletes `<skills_dir>/<skill>`. Exit `0` also when the skill isn't installed. Same
`--name` rules and capability handling as `skill-install`.

### `control-ui-auth`

Only with the `control-ui` capability; optional even then (missing verb or exit `3` means
no authentication). No arguments. Prints what the control plane must add to every
proxied Control UI request:

```json
{"query": {"token": "…"}, "headers": {"Origin": "http://localhost:18789"}}
```

Both fields are optional. The output is a credential: it is used by the control plane
and never sent to the browser as-is.

### `restart`

Restarts the agent service (`s6-svc -r /run/service/svc-agent` or equivalent). Exit `0`
when the restart was accepted; a no-op exit `0` is fine for agents with no daemon.

## Environment variables (set by the control plane)

| Variable | Purpose |
|---|---|
| `CLAWORC_INSTANCE_ID` | Instance identifier (existing) |
| `CLAWORC_CONNECTION_SECRET` | Secret for the internal proxy's Connections broker (see [connections.md](connections.md)) |
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
  back to its built-in native OpenClaw adapter (`internal/agentshim/openclawnative`).
  This path is **deprecated**: it exists only so pre-shim OpenClaw images keep working
  until they are updated, and it is the only place in the control plane with
  agent-specific knowledge. New features are built on the shim contract only.
- **shim-missing / shim-incompatible** — chat, config, and webhooks are disabled with
  an explanatory banner; terminal, file browser, logs, and VNC remain fully functional.

## Minimal chat-send

A bare-bones custom image can implement chat as a bash wrapper around any CLI agent
(`CHAT_CMD` reads the message on stdin and writes the reply to stdout):

```bash
#!/usr/bin/env bash
# /opt/claworc/shim/chat-send — minimal single-snapshot implementation
set -euo pipefail
source "$(dirname "$0")/lib/shimlib.sh"

parse_session_args "$@"
S=$(json_string "$SHIM_SESSION")
T=$(json_string "${SHIM_TURN:-t-$$}")

printf '{"v":1,"event":"start","session":%s,"turn":%s}\n' "$S" "$T"
if ! REPLY=$(su claworc -s /bin/bash -c "$CHAT_CMD" 2>/dev/null); then
  printf '{"v":1,"event":"end","turn":%s,"stop_reason":"error","text":""}\n' "$T"
  exit 0
fi
R=$(json_string "$REPLY")
printf '{"v":1,"event":"assistant","turn":%s,"message_id":"m1","text":%s}\n' "$T" "$R"
printf '{"v":1,"event":"end","turn":%s,"stop_reason":"complete","text":%s}\n' "$T" "$R"
```

`agent/template/` ships a complete copy-me implementation of every verb (PID file for
`chat-abort`, per-session transcripts, the managed `configure-llm` block, skills),
plus `shim-selftest`.
