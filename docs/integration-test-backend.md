# Backend Integration Tests

## Overview

These tests exercise the full Claworc server in-process against a real Docker daemon,
verifying end-to-end instance lifecycle behaviour (create → running → configure → delete).

## How to run

```sh
make test-integration-backend
```

By default the agent container image is whatever is configured as `default_container_image`
in the database. Override it per-run with:

```sh
AGENT_TEST_IMAGE=glukw/openclaw-vnc-chromium:local make test-integration-backend
```

## How the server is launched

`TestMain` calls `launchEmbeddedServer`, which:

1. Creates a temp directory for the SQLite database and SSH keys.
2. Sets `CLAWORC_AUTH_DISABLED=true` and `CLAWORC_DATA_PATH` in the process environment and calls `config.Load()`.
3. Initialises the database (GORM + SQLite), seeds `orchestrator_backend=docker`, and creates an `admin` user.
4. Finds a free TCP port and assigns it to `config.Cfg.LLMGatewayPort`.
5. Generates the global SSH key pair (`sshproxy.EnsureKeyPair`).
6. Wires up SSHManager, TunnelManager, SSH audit logger, terminal session manager, and session store — exactly as `main.go` does.
7. Initialises the Docker orchestrator (`orchestrator.InitOrchestrator`).
8. Starts the LLM gateway on the chosen port.
9. Registers background goroutines: SSH health checker, tunnel background manager, tunnel health checker, key rotation job.
10. Builds a minimal Chi router with only the routes the tests use and wraps it in `httptest.NewServer`.

The returned URL, cancel function, and cleanup function are stored at package level.

## Session reuse

One server is started for the entire test binary run. All `TestIntegration_*` functions share
`sessionURL` (the httptest base URL) and `sessionGatewayPort`.

Database and SSH state persist across tests within a single run. Each test is responsible for
creating its own instances and providers **and cleaning them up** via `defer` so subsequent tests
start from a consistent state.

## Adding a new test

Write a function with the signature:

```go
func TestIntegration_MyScenario(t *testing.T) {
    baseURL := sessionURL
    // use baseURL and sessionGatewayPort
}
```

No setup code is needed — the server is already running when your test executes.
Tag the file with `//go:build docker_integration` so it is excluded from the standard `go test` run.
