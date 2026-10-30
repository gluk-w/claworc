# Claworc E2E tests

On-demand browser tests that install Claworc fresh into a Kubernetes cluster
and exercise the dashboard via Playwright. Intended for **manual / on-demand**
runs from a developer machine, not CI/CD.

## Prerequisites

- `kubectl`, `helm`, `node` (with `npx`)
- A reachable Kubernetes cluster and a kubeconfig file that points at it
- Permission to create/destroy resources in the `claworc-e2e` namespace
- Built/published images for `claworc/claworc:latest` and the agent images
  (or set `E2E_BUILD=1` to rebuild before installing)

## Install

```sh
make e2e-install
```

This installs the Playwright dependency and Chromium.

## Run

```sh
./e2e/run.sh <path-to-kubeconfig>
# or
make e2e KUBECONFIG=./kubeconfig
```

Forward extra args to Playwright after `--`:

```sh
./e2e/run.sh ./kubeconfig -- --grep "smoke"
```

## What it does

1. Uninstalls any prior `claworc-e2e` release and wipes its PVCs (fresh data).
2. Optionally rebuilds + pushes images (`E2E_BUILD=1`).
3. `helm upgrade --install` into namespace `claworc-e2e` with
   `CLAWORC_AUTH_DISABLED=true` so the tests can skip login.
4. Waits for rollout, port-forwards `svc/claworc-e2e` to `localhost:18001`.
5. Runs Playwright against `http://localhost:18001`.
6. Tears everything down (skip with `E2E_KEEP=1`).

## Per-agent suites (`@e2e`)

`tests/agent/` runs once per agent type — Playwright projects `openclaw`,
`hermes` and `nanoclaw` — against one instance of that type that
`globalSetup` creates through the API (and `globalTeardown` deletes). Each
project covers: creating an agent from the dashboard, multi-turn chat,
terminal (incl. env vars), file upload, live log streaming, skill upload and
install, webhooks (a conversation spanning two requests), the VNC page, and a
config edit saved from the Config tab. The config spec runs last because
saving restarts the agent.

Chat and webhook specs make real LLM calls through a provider already
configured in the dashboard (default: key `anthropic`, model
`claude-haiku-4-5`); they skip when that provider doesn't exist.

Against a local dev environment (`make dev`, dashboard on :5173):

```sh
cd e2e
BASE_URL=http://localhost:5173 E2E_BROWSER_CHANNEL=chrome \
E2E_IMAGE_OPENCLAW=claworc-e2e-openclaw:dev \
E2E_IMAGE_HERMES=claworc-e2e-hermes:dev \
E2E_IMAGE_NANOCLAW=claworc-e2e-nanoclaw:dev \
  npx playwright test --project openclaw --project hermes --project nanoclaw --grep @e2e
```

Build the images first, e.g.
`docker buildx build --platform linux/arm64 -t claworc-e2e-hermes:dev -f agent/hermes/Dockerfile --load agent/hermes/`.

## Environment toggles

| Var | Default | Effect |
|---|---|---|
| `E2E_BUILD` | unset | When set, rebuilds + pushes control-plane and agent images first |
| `E2E_KEEP` | unset | When set, skips uninstall after the run for debugging |
| `E2E_HELM_ARGS` | empty | Extra args passed to `helm upgrade --install` |
| `LOCAL_PORT` | `18001` | Local port used for the kubectl port-forward |
| `BASE_URL` | `http://localhost:18001` | Dashboard URL the suites run against |
| `E2E_AGENT_TYPES` | all three | Comma-separated agent types `globalSetup` creates instances for |
| `E2E_IMAGE_<TYPE>` | dashboard default | Container image override per type (`OPENCLAW`, `HERMES`, `NANOCLAW`) |
| `E2E_PROVIDER_KEY` / `E2E_MODEL` | `anthropic` / `claude-haiku-4-5` | LLM provider key and model wired onto the test instances |
| `E2E_TEAM_ID` | first team | Team the test instances are created in |
| `E2E_BROWSER_CHANNEL` | bundled Chromium | e.g. `chrome` to use the installed Google Chrome |
| `E2E_KEEP_INSTANCES` | unset | Keep the per-type instances after the run |
| `E2E_REUSE_INSTANCES` | unset | Reuse still-running instances kept by a previous run |

## Reports

- HTML report at `e2e/playwright-report/index.html` (`npm run report`)
- Traces / videos for failed tests under `e2e/test-results/`
