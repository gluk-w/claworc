# Adding an Agent

Claworc manages any container image that implements the [agent shim contract](shim.md).
The control plane never special-cases an agent: everything it does (chat, webhooks,
Kanban, config editing, LLM routing, skills, Control UI, restart) is driven by the
image's shim verbs and the capabilities its `meta` verb declares.

There are two ways to bring a new agent in.

## 1. Custom image (no control-plane change)

1. Copy `agent/template/` and follow its [README](../agent/template/README.md): install
   the agent in the `Dockerfile`, point `shim/agent.env`'s `CHAT_CMD` at it, trim the
   `capabilities` in `shim/meta`, and adapt `configure-llm` to the agent's native
   config.
2. Validate from inside the image:
   ```sh
   docker build -t my-agent .
   docker run --rm my-agent /opt/claworc/shim/shim-selftest
   ```
3. Push the image to a registry the cluster/Docker host can pull from.
4. In the dashboard, create an agent with type **Custom** and your image. Admins can
   set a default image for the Custom type in Settings (`default_agent_images.custom`).

Features follow the declared capabilities: an image without `config` gets no Config
tab, without `skills` gets no skill deployment, without `control-ui` gets no Control UI
link. `chat` is the only mandatory capability.

## 2. First-class agent (shipped in this repo)

What Hermes and NanoClaw did, in order:

| Step | Where |
|---|---|
| Image with shim under `/opt/claworc/shim/`; `lib/shimlib.sh` byte-identical to the template's | `agent/<agent>/` |
| Integration suite: `shim-contract.ts` helpers + agent-specific cases | `agent/tests/<agent>.test.ts`, image name in `agent/tests/global-setup.ts` |
| Build, test, and push targets | `Makefile` (`agent-build`, `agent-shim-test`, `agent-test`, `agent-push`) |
| CI gate and publishing | `.github/workflows/agent.yml` |
| Registry entry: type id, display name, pre-probe capability placeholder | `control-plane/internal/agentshim/registry.go` (`registryEntries`, `Validate` message) |
| Default image seed | `default_agent_images` in `control-plane/internal/database/database.go` |

No adapter code is needed: every non-OpenClaw type goes through the generic
`shimexec` client (`internal/agentshim/factory.go`). The per-type `openclawnative`
adapter exists only to keep pre-shim OpenClaw images working.

If the agent needs extra env vars that predate the contract, register them with
`agentshim.RegisterLegacyEnv` so they are reserved — see
[environment-variables.md](environment-variables.md#reserved-names). New agents should
use the `CLAWORC_*` contract variables instead.

## Checklist

- `meta` declares `contract`, `chat`, and only the capabilities actually implemented.
- `configure-llm` is idempotent and handles the `<provider>/<model>` id format
  ([shim.md § configure-llm](shim.md#configure-llm)).
- The image applies `CLAWORC_INITIAL_LLM_CONFIG` at first boot.
- Agent state lives under `/home/claworc` (the persistent volume) and is owned by the
  `claworc` user.
- `shim-selftest` passes; the image builds for both `linux/amd64` and `linux/arm64`.
