# Phase 01: Agent Image Build & Test

This phase builds the OpenClaw agent Docker image for linux/amd64, runs the Vitest test suite against it, and fixes any failures. By the end, you'll have a tested agent image proving that OpenClaw configuration, gateway startup, and Chromium integration all work correctly inside the LinuxServer.io-based container. This is the core validation step before publishing.

## Context

- **Branch**: `docker-checks` — has uncommitted changes migrating from a custom Ubuntu base to `lscr.io/linuxserver/chromium:latest`
- **Key changes in working tree**: image renamed to `openclaw-vnc-chromium2`, user changed from `claworc` to `abc`, config path moved to `/config/.openclaw/`, s6-overlay service restructured, `cont-init.d` moved to `custom-cont-init.d`
- **Makefile targets**: `agent-build` (buildx amd64 --load), `agent-test` (vitest via npm)
- **Test suite**: `tests/agent/openclaw.test.ts` — spins up a privileged container, polls for gateway startup, verifies config structure, CLI commands, and gateway reachability
- **Snapshot**: `tests/agent/__snapshots__/openclaw.test.ts.snap` — captures `openclaw.json` structure

## Tasks

- [x] Verify build prerequisites and install test dependencies:
  - Run `docker info` to confirm Docker daemon is accessible
  - Run `docker buildx ls` to confirm a builder with `linux/amd64` support exists (the `multiarch` builder should be active)
  - Check that `tests/node_modules` exists; if not, run `cd tests && npm install`
  - Verify the Makefile has `AGENT_IMAGE_NAME := openclaw-vnc-chromium2` (it should — the working tree already has this change)
  > All prerequisites verified: Docker Engine 29.2.1, `multiarch` builder active with linux/amd64, test node_modules present, Makefile confirmed.

- [x] Build the agent image for linux/amd64 by running `make agent-build` from the repo root (`/Users/stan/claworc`). This executes `docker buildx build --platform linux/amd64 --load -t openclaw-vnc-chromium2:test agent/`. The build pulls `lscr.io/linuxserver/chromium:latest`, installs Node 22, OpenClaw, Poetry, and copies rootfs overlay files. Watch for:
  - Base image pull failures (network issues)
  - `npm install -g openclaw@latest` failures (registry issues)
  - `COPY` failures if expected files are missing from `agent/` directory
  - The `chmod +x` on `custom-cont-init.d/50-force-autostart` — this path was recently changed from `cont-init.d`
  - If the build fails, read the error output, diagnose, fix the relevant file in `agent/`, and re-run
  > Build succeeded. Image loaded as `openclaw-vnc-chromium2:test`. OpenClaw 2026.2.19-2 installed, Poetry 2.3.2, Node 22.22.0.

- [x] Run the agent test suite by executing `make agent-test` from the repo root. This runs `cd tests && AGENT_TEST_IMAGE=openclaw-vnc-chromium2:test npm run test:agent` which invokes `vitest run agent/`. The test:
  - Starts a privileged container with `OPENCLAW_GATEWAY_TOKEN=zzzbbb`
  - Polls up to 120s for `openclaw gateway` process, then 30s for port 18789
  - Checks `/config/.openclaw` ownership (abc:abc)
  - Validates `openclaw.json` structure against snapshot
  - Tests CLI config commands and gateway reachability
  - **Important**: Use a timeout of at least 300000ms (5 min) for this command since the container startup + gateway init takes time, especially under amd64 emulation on ARM hosts
  > Initial run: 6 passed, 2 failed (snapshot mismatch + `openclaw status` security error). See fix task below.

- [x] If any tests fail, diagnose and fix:
  - **Snapshot mismatch**: The `openclaw.json` structure may have changed if the installed `openclaw@latest` version differs from when the snapshot was last captured. Run `cd tests && npx vitest run agent/ --update` to update the snapshot, then review the diff in `tests/agent/__snapshots__/openclaw.test.ts.snap` to confirm the new structure is reasonable
  - **Gateway startup timeout**: Check container logs with `docker logs agent-test-<PID>` (the PID is from the test process). Look for s6-overlay init errors, missing permissions, or OpenClaw crash output. The `svc-openclaw/run` script sets `HOME=/config` and runs gateway as user `abc`
  - **Ownership failures**: The `custom-cont-init.d/50-force-autostart` script should create and chown `/config/.openclaw` to `abc:abc`. If this fails, the init script may not be executing — verify it's marked executable and in the right path
  - **Config command failures**: If `openclaw config set` fails, the gateway may not be fully initialized. Check that the gateway WebSocket on port 18789 is actually listening
  - After fixing, re-run `make agent-test` to confirm all tests pass
  > **Two fixes applied:**
  > 1. **Snapshot updated** — openclaw@latest (2026.2.19-2) changed config structure: removed `agents`, `messages`, `wizard` top-level keys; added `commands.restart`. Updated via `vitest --update`.
  > 2. **Gateway bind changed from `lan` to `localhost`** in `agent/rootfs/etc/s6-overlay/s6-rc.d/svc-openclaw/run`. OpenClaw 2026.2.19+ enforces a security check rejecting `ws://` to non-loopback addresses. With `bind: lan`, the CLI constructed `ws://172.17.0.4:18789` which failed. Changing to `localhost` makes the CLI use `ws://127.0.0.1:18789`. External access is unaffected because the control plane connects through nginx on port 3000, which reverse-proxies to `127.0.0.1:18789` internally.

- [x] Once all tests pass, verify the test results show all 8 test cases passing:
  - `openclaw home directory exists and is owned by abc`
  - `openclaw.json structure matches snapshot`
  - `openclaw gateway process is running`
  - `openclaw logs exits without crash`
  - `can set gateway auth token via config`
  - `can set agents.defaults.model via --json`
  - `openclaw status shows gateway as reachable`
  - `openclaw gateway stop exits without crash`
  - Print a summary of the test output confirming the pass count
  > **All 8 tests passed.** Duration: 119.71s. Test Files: 1 passed (1). Tests: 8 passed (8).
