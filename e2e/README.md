# Claworc E2E tests

On-demand browser tests that install Claworc fresh into a Kubernetes cluster
and exercise the dashboard via Playwright. Intended for **manual / on-demand**
runs from a developer machine, not CI/CD.

## Prerequisites

- `kubectl`, `helm`, `node` (with `npx`)
- A reachable Kubernetes cluster and a kubeconfig file that points at it
- Permission to create/destroy resources in the `claworc-e2e` namespace
- Built/published images for `glukw/claworc:latest` and the agent images
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

## Environment toggles

| Var | Default | Effect |
|---|---|---|
| `E2E_BUILD` | unset | When set, rebuilds + pushes control-plane and agent images first |
| `E2E_KEEP` | unset | When set, skips uninstall after the run for debugging |
| `E2E_HELM_ARGS` | empty | Extra args passed to `helm upgrade --install` |
| `LOCAL_PORT` | `18001` | Local port used for the kubectl port-forward |

## Reports

- HTML report at `e2e/playwright-report/index.html` (`npm run report`)
- Traces / videos for failed tests under `e2e/test-results/`
