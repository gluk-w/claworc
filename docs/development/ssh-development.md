# SSH Components Developer Guide

This guide covers how to work with the SSH subsystem in Claworc: architecture, adding new functionality, testing strategies, and debugging.

## Package Overview

The SSH subsystem spans four packages:

| Package | Path | Responsibility |
|---------|------|---------------|
| `sshproxy` | `internal/sshproxy/` | Core SSH: keys, connections, tunnels, health, reconnection, state, events, rate limiting, IP restrictions, file ops, log streaming |
| `sshkeys` | `internal/sshkeys/` | Global key pair rotation across all instances |
| `sshaudit` | `internal/sshaudit/` | Persistent SSH audit logging (SQLite-backed) |
| `sshterminal` | `internal/sshterminal/` | Interactive terminal sessions over SSH with persistence |

### File Map (`internal/sshproxy/`)

| File | Purpose |
|------|---------|
| `keys.go` | ED25519 key pair generation, persistence, loading |
| `manager.go` | `SSHManager` — one multiplexed connection per instance |
| `tunnel.go` | `TunnelManager` — reverse SSH tunnels over managed connections |
| `health.go` | Application-level health monitoring (`echo ping` every 30s) |
| `reconnect.go` | Automatic reconnection with exponential backoff |
| `tunnel_health.go` | TCP-level tunnel health monitoring (every 60s) |
| `state.go` | Per-instance connection state machine with ring buffer history |
| `events.go` | Per-instance connection event log (ring buffer, 100 entries) |
| `ratelimit.go` | Two-tier connection rate limiting per instance |
| `iprestrict.go` | Per-instance source IP whitelisting |
| `files.go` | Remote file operations via SSH exec |
| `logs.go` | Remote log streaming via `tail` over SSH |

## How the Single Global Key Pair Works

Claworc uses **one ED25519 key pair** for all SSH connections:

1. **Auto-generation on first startup**: `sshproxy.EnsureKeyPair(dataDir)` checks for `{data_dir}/ssh_key` and `{data_dir}/ssh_key.pub`. If they don't exist, it generates a new pair and writes them to disk (private key mode `0600`, public key mode `0644`).

2. **On-demand key upload**: When connecting to an instance, `SSHManager.EnsureConnected` calls `Orchestrator.ConfigureSSHAccess(instanceID, publicKey)` to upload the public key to the agent's `~/.ssh/authorized_keys` before dialing SSH.

3. **Reconnection re-uploads**: Each reconnection attempt re-uploads the public key via `ConfigureSSHAccess` because the agent container may have restarted, losing the `authorized_keys` file.

4. **Key rotation**: The `sshkeys.RotateGlobalKeyPair` function safely rotates the key pair across all running instances in a multi-step process (see `internal/sshkeys/rotation.go`).

```go
// Startup sequence (from main.go)
sshSigner, sshPublicKey, err := sshproxy.EnsureKeyPair(config.Cfg.DataPath)
sshMgr := sshproxy.NewSSHManager(sshSigner, sshPublicKey)
```

Key files live in the same directory as the SQLite database (`CLAWORC_DATA_PATH`, default `/app/data`):
```
/app/data/
├── ssh_key          # ED25519 private key (PEM, mode 0600)
├── ssh_key.pub      # Public key (OpenSSH authorized_keys format, mode 0644)
└── claworc.db       # SQLite database
```

## How to Add New SSH-Based Functionality

### Adding a New Remote Command

Remote commands are executed over SSH using `executeCommand` in `files.go`. Follow this pattern:

```go
// In sshproxy/files.go or a new file in the sshproxy package

func YourNewOperation(client *ssh.Client, arg string) (string, error) {
    // Always shell-quote user-provided arguments
    cmd := fmt.Sprintf("your-command %s", shellQuote(arg))
    stdout, stderr, exitCode, err := executeCommand(client, cmd)
    if err != nil {
        return "", fmt.Errorf("your operation: %w", err)
    }
    if exitCode != 0 {
        return "", fmt.Errorf("your operation failed (exit %d): %s", exitCode, stderr)
    }
    return stdout, nil
}
```

Key rules:
- Always use `shellQuote()` for user-provided values to prevent shell injection.
- Use `executeCommand` for one-shot commands (it creates and closes an SSH session).
- Use `executeCommandWithStdin` when you need to pipe data into a command.
- The `ssh.Client` is obtained from `SSHManager.GetConnection(instanceID)` or `SSHManager.EnsureConnected(ctx, instanceID, orch)`.

### Adding a New Tunnel Type

Tunnels are created via `TunnelManager.CreateReverseTunnel`. To add a new service tunnel:

```go
// In sshproxy/tunnel.go

// CreateTunnelForMyService creates a reverse tunnel for MyService (port 9090).
func (tm *TunnelManager) CreateTunnelForMyService(ctx context.Context, instanceID uint) (int, error) {
    port, err := tm.CreateReverseTunnel(ctx, instanceID, "MyService", 9090, 0)
    if err != nil {
        return 0, fmt.Errorf("create MyService tunnel for instance %d: %w", instanceID, err)
    }
    log.Printf("MyService tunnel for instance %d: localhost:%d -> agent:9090", instanceID, port)
    return port, nil
}

// GetMyServiceLocalPort returns the local port for the MyService tunnel, or 0 if not found.
func (tm *TunnelManager) GetMyServiceLocalPort(instanceID uint) int {
    tm.mu.RLock()
    defer tm.mu.RUnlock()
    for _, t := range tm.tunnels[instanceID] {
        if t.Label == "MyService" && t.Status == "active" {
            return t.LocalPort
        }
    }
    return 0
}
```

Then add the new tunnel to `StartTunnelsForInstance`:

```go
// In StartTunnelsForInstance, after existing tunnel creation:
_, err = tm.CreateTunnelForMyService(ctx, instanceID)
if err != nil {
    log.Printf("Failed to create MyService tunnel for instance %d: %v", instanceID, err)
}
```

Parameters:
- `label`: Human-readable name used to look up the tunnel later (must be unique per instance).
- `remotePort`: Port on the agent that the tunnel forwards to.
- `localPort`: Pass `0` to auto-allocate a free local port (recommended).

The background reconciler (`StartBackgroundManager`) will automatically maintain the new tunnel — no additional reconciliation code is needed.

### Extending Health Monitoring

Health monitoring has two layers. To add a new layer:

**Connection-level** (`health.go`): Runs every 30s, executes `echo ping` over SSH.
**Tunnel-level** (`tunnel_health.go`): Runs every 60s, does a TCP probe to each tunnel's local port.

To add a custom health check (e.g., verifying a service is responding):

```go
// In sshproxy/health.go or a new file

func (m *SSHManager) ServiceHealthCheck(instanceID uint) error {
    m.mu.RLock()
    mc, ok := m.conns[instanceID]
    m.mu.RUnlock()
    if !ok {
        return fmt.Errorf("no connection for instance %d", instanceID)
    }

    session, err := mc.client.NewSession()
    if err != nil {
        return fmt.Errorf("create session: %w", err)
    }
    defer session.Close()

    // Run a service-specific probe
    output, err := session.Output("curl -sf http://localhost:9090/health")
    if err != nil {
        return fmt.Errorf("service health check failed: %w", err)
    }
    // Parse output as needed
    _ = output
    return nil
}
```

To integrate it into the background checker, add the check inside `checkAllConnections()` in `health.go`.

### Adding a New Event Type

Connection events are defined in `reconnect.go` and recorded in the event log (`events.go`):

```go
// 1. Define the event type constant (in reconnect.go or events.go)
const EventMyCustomEvent ConnectionEventType = "my_custom_event"

// 2. Emit the event from the relevant code path
m.emitEvent(ConnectionEvent{
    InstanceID: instanceID,
    Type:       EventMyCustomEvent,
    Timestamp:  time.Now(),
    Details:    "descriptive details",
})
```

Events are stored in a per-instance ring buffer (100 entries) and also dispatched to registered `EventListener` callbacks (e.g., the audit logger).

## The Orchestrator Interface

The `Orchestrator` interface decouples SSH management from the container runtime:

```go
type Orchestrator interface {
    ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error
    GetSSHAddress(ctx context.Context, instanceID uint) (host string, port int, err error)
}
```

Both the Kubernetes orchestrator (`internal/orchestrator/kubernetes.go`) and Docker orchestrator (`internal/orchestrator/docker.go`) implement this interface. In tests, you create mock implementations:

```go
type mockOrch struct {
    configureErr error
    host         string
    port         int
    addressErr   error
}

func (m *mockOrch) ConfigureSSHAccess(ctx context.Context, instanceID uint, publicKey string) error {
    return m.configureErr
}

func (m *mockOrch) GetSSHAddress(ctx context.Context, instanceID uint) (string, int, error) {
    return m.host, m.port, m.addressErr
}
```

## Initialization and Wiring

The SSH subsystem is initialized in `main.go` in a specific order:

```go
// 1. Key pair (auto-generates on first run)
sshSigner, sshPublicKey, err := sshproxy.EnsureKeyPair(config.Cfg.DataPath)

// 2. SSH connection manager
sshMgr := sshproxy.NewSSHManager(sshSigner, sshPublicKey)

// 3. Tunnel manager (depends on SSHManager)
tunnelMgr := sshproxy.NewTunnelManager(sshMgr)

// 4. Audit logger (bridges SSH events to persistent log)
auditor, _ := sshaudit.NewAuditor(database.DB, retentionDays)
sshMgr.OnEvent(func(event sshproxy.ConnectionEvent) {
    // Map SSH events to audit log entries
})

// 5. Terminal session manager
termMgr := sshterminal.NewSessionManager(sshterminal.SessionManagerConfig{...})

// 6. Wire orchestrator for auto-reconnect
sshMgr.SetOrchestrator(orch)
sshMgr.StartHealthChecker(ctx)

// 7. Background tunnel reconciler + tunnel health checker
tunnelMgr.StartBackgroundManager(ctx, listRunningFn, orch)
tunnelMgr.StartTunnelHealthChecker(ctx)

// 8. Graceful shutdown (reverse order)
termMgr.Stop()
tunnelMgr.StopAll()
sshMgr.CloseAll()
```

Dependencies are injected as package-level variables in the `handlers` package: `handlers.SSHMgr`, `handlers.TunnelMgr`, `handlers.AuditLog`, `handlers.TermSessionMgr`.

## Testing Strategies

### Unit Tests (In-Process SSH Server)

Unit tests use an in-process SSH server defined in `manager_test.go`. This avoids Docker dependencies and runs fast:

```go
func TestYourFeature(t *testing.T) {
    // Generate a key pair for the test
    signer, ts := newTestSignerAndServer(t)
    defer ts.cleanup()

    mgr := NewSSHManager(signer, string(ssh.MarshalAuthorizedKey(signer.PublicKey())))

    // Parse host/port from the test server
    host, port := parseHostPort(t, ts.addr)

    // Connect
    client, err := mgr.Connect(context.Background(), 1, host, port)
    if err != nil {
        t.Fatalf("connect: %v", err)
    }

    // Your assertions here
    _ = client
}
```

The `testSSHServer` function:
1. Generates a host key pair.
2. Configures `ssh.ServerConfig` with public key authentication.
3. Listens on `127.0.0.1:0` (random port).
4. Accepts connections in a goroutine, handling SSH handshake and sessions.

### Overriding Timing Constants

Package-level vars allow tests to run fast without waiting for real timeouts:

```go
func TestReconnectBackoff(t *testing.T) {
    // Override backoff timing for fast tests
    origInitial := reconnectInitialBackoff
    origMax := reconnectMaxBackoff
    reconnectInitialBackoff = 10 * time.Millisecond
    reconnectMaxBackoff = 50 * time.Millisecond
    defer func() {
        reconnectInitialBackoff = origInitial
        reconnectMaxBackoff = origMax
    }()

    // Test proceeds with fast backoff...
}
```

Overridable vars:
- `reconnectInitialBackoff`, `reconnectMaxBackoff`, `reconnectDefaultRetries` (in `reconnect.go`)
- `tunnelHealthCheckInterval`, `tunnelHealthCheckTimeout` (in `tunnel_health.go`)

### Integration Tests (Docker)

Integration tests use the `docker_integration` build tag and start real agent containers:

```bash
# Build the agent image first
make agent-build

# Run integration tests
go test -tags docker_integration -v ./internal/sshproxy/ -timeout 300s

# Override agent image
AGENT_TEST_IMAGE=myregistry/agent:dev go test -tags docker_integration ...
```

Integration tests use a `dockerTestEnv` helper that:
1. Creates a Docker client and verifies connectivity.
2. Starts agent containers with SSH port mappings.
3. Waits for sshd to become ready (polling with timeout).
4. Cleans up containers in `t.Cleanup`.

Example:

```go
//go:build docker_integration

func TestIntegrationSSHConnect(t *testing.T) {
    env := newDockerTestEnv(t)
    defer env.cleanup(t)

    inst := env.startAgent(t, "test-agent")

    signer, pubKey := generateTestKeyPair(t)
    env.uploadPublicKey(t, inst, pubKey)

    mgr := NewSSHManager(signer, pubKey)
    client, err := mgr.Connect(context.Background(), 1, inst.sshHost, inst.sshPort)
    if err != nil {
        t.Fatalf("connect: %v", err)
    }
    defer client.Close()

    // Execute a command to verify
    session, _ := client.NewSession()
    output, _ := session.Output("hostname")
    t.Logf("hostname: %s", output)
}
```

### What to Test

| Category | What to Test | Example |
|----------|-------------|---------|
| Connection lifecycle | Connect, disconnect, reconnect, concurrent access | `manager_test.go` |
| Tunnel management | Create, reuse, stop, reconcile, backoff | `tunnel_test.go` |
| Health checks | Success, failure, metrics update | `health_test.go` |
| Reconnection | Backoff timing, event emission, key re-upload | `reconnect_test.go` |
| State machine | Transitions, ring buffer, callbacks | `state_test.go` |
| Event logging | Recording, ring buffer, retrieval | `events_test.go` |
| Rate limiting | Window rate, failure blocking, escalation | `ratelimit_test.go` |
| IP restrictions | Parse, allow, deny, CIDR matching | `iprestrict_test.go` |
| Security | Concurrent attack simulation, input validation | `security_test.go`, `hardening_test.go` |
| Resilience | Failure recovery, tunnel recreation | `resilience_test.go` |
| File operations | List, read, write, mkdir | `files_test.go` |
| Log streaming | Stream, resolve paths, shell quoting | `logs_test.go` |

### Running Tests

```bash
# Unit tests only (no Docker required)
cd control-plane
go test ./internal/sshproxy/ -v
go test ./internal/sshkeys/ -v
go test ./internal/sshaudit/ -v
go test ./internal/sshterminal/ -v

# Integration tests (requires Docker + agent image)
go test -tags docker_integration ./internal/sshproxy/ -v -timeout 300s

# Run benchmarks
go test ./internal/sshproxy/ -bench=. -benchmem

# Run a specific test
go test ./internal/sshproxy/ -run TestReconnectWithBackoff -v
```

## Debugging SSH Connection Issues

### Enable Verbose Logging

All SSH operations log to the standard Go `log` package. Key log patterns to look for:

```
SSH connected to instance 42 (10.0.0.5:22)         # Successful connection
SSH keepalive failed for instance 42: ...            # Dead connection detected
SSH reconnecting to instance 42 (reason: ...)        # Reconnection started
SSH reconnect attempt 3/10 for instance 42           # Reconnection progress
SSH key upload failed for instance 42: ...           # Key upload issue
SSH reconnection to instance 42 failed after 10 attempts  # Gave up
```

### Inspect Connection State via API

```bash
# Connection events (ring buffer, 100 entries per instance)
curl http://localhost:8000/api/v1/instances/42/ssh-events

# Connection state transitions (ring buffer, 50 entries per instance)
# Available via SSHManager.GetStateTransitions(instanceID) in code

# Connection health metrics
# Available via SSHManager.GetMetrics(instanceID) in code

# Audit log (persistent, SQLite-backed)
curl "http://localhost:8000/api/v1/audit-logs?instance_id=42&event_type=connection&limit=50"
```

### Common Issues and Solutions

**Connection refused**
- The agent's sshd may not be running. Check agent logs: `docker exec <container> journalctl -u sshd`.
- The SSH port may not be exposed. Verify with `docker port <container>` or check the Kubernetes service.

**Authentication failed**
- The public key may not be in `authorized_keys`. This happens when the agent container restarts. The reconnection logic re-uploads the key automatically, but manual operations need explicit key upload via `ConfigureSSHAccess`.
- Check the key fingerprint: `SSHManager.GetPublicKeyFingerprint()` vs what's in the agent's `~/.ssh/authorized_keys`.

**Connection drops after a while**
- Check the health checker logs. It runs every 30s and removes dead connections.
- The keepalive interval is 30s. If the network drops packets silently, connections may survive up to 30s before detection.
- Look at `ConnectionMetrics.FailedChecks` to see if health checks are failing intermittently.

**Tunnels not working**
- Verify the SSH connection is alive first: `SSHManager.IsConnected(instanceID)`.
- Check tunnel status: `TunnelManager.GetTunnelsForInstance(instanceID)` — look at the `Status` field.
- The tunnel health checker runs every 60s. Between checks, a tunnel may be in an error state.
- Ensure the remote service is listening on the expected port inside the agent container.

**Rate limited**
- After 5 consecutive connection failures, an instance is temporarily blocked (30s, escalating to 5min).
- Check rate limiter state: `SSHManager.RateLimiter().GetState(instanceID)`.
- Reset manually if needed: `SSHManager.RateLimiter().Reset(instanceID)`.

**IP restriction blocking**
- The control plane determines its outbound IP via a UDP probe to the target. In containerized environments, the outbound IP may differ from expected.
- Check with: `sshproxy.GetOutboundIP(host, port)`.
- Parse and test restrictions: `sshproxy.ParseIPRestrictions(csv)` then `restriction.IsAllowed(ip)`.

## Best Practices for SSH Error Handling

### Always Wrap Errors with Context

```go
// Good: error chain tells you what operation failed and for which instance
return fmt.Errorf("create VNC tunnel for instance %d: %w", instanceID, err)

// Bad: loses context about which instance and what operation
return err
```

### Use the Error Types

The package defines specific error types for programmatic error handling:

```go
// Rate limiting
var rateLimitErr *ErrRateLimited
if errors.As(err, &rateLimitErr) {
    log.Printf("Instance %d rate limited, retry after %s", rateLimitErr.InstanceID, rateLimitErr.RetryAfter)
}

// IP restriction
var ipErr *ErrIPRestricted
if errors.As(err, &ipErr) {
    log.Printf("Instance %d blocked: source IP %s not allowed", ipErr.InstanceID, ipErr.SourceIP)
}
```

### Don't Retry Inside Low-Level Functions

Low-level functions (`Connect`, `HealthCheck`, `CreateReverseTunnel`) should fail fast. Retry logic lives in the reconnection layer (`ReconnectWithBackoff`, `triggerReconnect`) and the tunnel reconciler (`reconcile`).

### Emit Events for Observability

When adding new failure modes, emit events so they're visible in the event log and audit trail:

```go
m.emitEvent(ConnectionEvent{
    InstanceID: instanceID,
    Type:       EventDisconnected,
    Timestamp:  time.Now(),
    Details:    fmt.Sprintf("your failure reason: %v", err),
})
```

## Concurrency Model

The SSH subsystem is designed for concurrent access. Key mutex rules:

| Mutex | Protects | File |
|-------|----------|------|
| `SSHManager.keyMu` | `signer`, `publicKey` (during key rotation) | `manager.go` |
| `SSHManager.mu` | `conns` map | `manager.go` |
| `SSHManager.reconnMu` | `orch`, `eventListeners`, `reconnecting` map | `reconnect.go` |
| `TunnelManager.mu` | `tunnels` map | `tunnel.go` |
| `TunnelManager.backoffMu` | `backoff` map | `tunnel.go` |
| `stateTracker.mu` | per-instance state + transition history | `state.go` |
| `eventLog.mu` | per-instance event buffers | `events.go` |
| `RateLimiter.mu` | per-instance rate state | `ratelimit.go` |

Each concern has its own mutex to avoid deadlocks. Never hold multiple mutexes simultaneously. The general pattern is: acquire lock, copy data, release lock, then work with the copy.
