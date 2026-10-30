# Claworc Custom Agent Template

Copy-me skeleton for building a custom agent image that Claworc can manage —
chat, config editing, LLM virtual-key routing, health checks, and webhooks all
work through the [Claworc Agent Shim Contract](../../docs/shim.md) implemented
here as small POSIX shell scripts.

## What's inside

```
Dockerfile                  debian-slim + s6-overlay + hardened sshd (no agent)
rootfs/
  etc/ssh/sshd_config.d/    hardened sshd config (SSH is the only hard runtime dep)
  etc/s6-overlay/           init-setup oneshot (env propagation, /var/log/claworc,
                            first-boot CLAWORC_INITIAL_LLM_CONFIG) + svc-sshd
shim/                       installed at /opt/claworc/shim
  agent.txt agent.svg       static identity (name + square logo), read over SFTP
  agent.env                 your agent's config: CHAT_CMD + managed LLM block
  meta                      capability probe (static JSON heredoc)
  health                    0 ready / 4 booting / 1 broken
  chat-send                 CHAT_CMD wrapper -> normalized chat JSONL
  chat-abort                best-effort SIGTERM of the running chat-send
  session-reset             removes the per-session transcript
  config-get / config-set   raw agent.env bytes (config-set validates + tmp/mv)
  configure-llm             rewrites the managed LLM block in agent.env
  restart                   no-op (no daemon); swap for s6-svc -r if you add one
  shim-selftest             contract conformance check — run it in CI
```

## Make it yours

1. **Install your agent** in the marked section of the `Dockerfile`.
2. **Point `shim/agent.env`'s `CHAT_CMD`** at your agent's CLI. The contract:
   it reads the user message on stdin and writes the reply to stdout. The
   default `cat` is an echo agent so the image passes `shim-selftest` as-is.
3. **Update `shim/meta`**: agent name/version, and trim `capabilities` to what
   you actually support (`chat` is mandatory; drop `configure-llm` etc. if
   they don't apply — the Claworc UI hides those features).
4. **LLM routing**: `configure-llm` writes `OPENAI_BASE_URL`/`OPENAI_API_KEY`
   (or the Anthropic pair) plus generic `CLAWORC_LLM_*` variables into the
   managed block of `agent.env`, pointing at the Claworc LLM proxy with a
   virtual key. Make your `CHAT_CMD` honor those variables, or adapt the verb
   to your agent's native config format (keep it idempotent: rewrite a fully
   managed section, never append).
5. **Streaming (optional)**: the stock `chat-send` emits a single cumulative
   assistant snapshot when `CHAT_CMD` finishes. If your agent streams, emit
   snapshot lines as output accumulates — `assistant.text` is always the full
   text so far, never a delta.
6. **Session history (optional)**: the template declares
   `session_persistence: "none"` — each turn is fresh. Feed the transcript
   kept in `/home/claworc/.claworc/shim/sessions/` back into your agent and
   declare `"emulated"` if you want multi-turn memory.

## Validate

```sh
docker build -t my-agent .
docker run --rm my-agent /opt/claworc/shim/shim-selftest
```

`shim-selftest` checks the identity files, `meta` JSON, `health` exit codes,
`chat-send` JSONL well-formedness (every line parses, exactly one `end`
event, last), the config round-trip, and `configure-llm` idempotency, and
exits non-zero with a per-check report on any failure. The config and
configure-llm checks mutate agent configuration — run in a throwaway
container, or pass `--skip-mutating`. It can also run against a shim
directory outside a container: `shim/shim-selftest ./shim`.

For a full-featured reference implementation (daemon agent, gateway bridge,
native sessions), see `agent/openclaw/`.
