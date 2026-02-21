---
type: guide
title: SSH Component Development Guide
created: 2026-02-21
tags:
  - ssh
  - development
  - testing
  - patterns
related:
  - "[[ssh-connectivity]]"
  - "[[ssh-configuration]]"
  - "[[ssh-operations]]"
---

# SSH Component Development Guide

This guide covers how to develop, test, and extend the SSH subsystem in the OpenClaw Orchestrator. It is intended for developers adding new SSH-based functionality, writing tests, or debugging SSH components during development.

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Package Reference](#2-package-reference)
3. [Adding New SSH-Based Functionality](#3-adding-new-ssh-based-functionality)
4. [Adding New Tunnel Types](#4-adding-new-tunnel-types)
5. [Extending Health Monitoring](#5-extending-health-monitoring)
6. [Testing SSH Components](#6-testing-ssh-components)
7. [Debugging SSH Issues During Development](#7-debugging-ssh-issues-during-development)
8. [Error Handling Best Practices](#8-error-handling-best-practices)
9. [Code Patterns Reference](#9-code-patterns-reference)

---

## 1. Architecture Overview

All control-plane-to-agent communication uses SSH. The control plane is the SSH client; each agent runs sshd. The SSH subsystem is organized into 7 packages under `control-plane/internal/`:

```
Control Plane (SSH Client)                    Agent (SSH Server)
┌─────────────────────────┐                  ┌──────────────────┐
│  sshkeys     → Key generation & rotation   │                  │
│  sshmanager  → Connection pool & health    │  sshd (OpenSSH)  │
│  sshtunnel   → Port-forwarding tunnels  ──────► VNC :3000     │
│  sshfiles    → Remote file ops (exec)   ──────► filesystem    │
│  sshlogs     → Log streaming (exec)     ──────► log files     │
│  sshterminal → Interactive PTY sessions ──────► /bin/bash      │
│  sshaudit    → Security audit logging      │                  │
└─────────────────────────┘                  └──────────────────┘
```

**Key architectural principle**: All packages share SSH connections via `sshmanager`. A single `*ssh.Client` per instance is reused for tunnels, exec sessions, and PTY sessions through SSH's built-in multiplexing.

### Global Singleton Registry

During application startup, `sshtunnel.InitGlobal()` creates the shared instances:

```go
// control-plane/internal/sshtunnel/registry.go
func InitGlobal() {
    globalSSHManager = sshmanager.NewSSHManager(0)
    globalTunnelManager = NewTunnelManager(globalSSHManager)
    globalSessionManager = sshterminal.NewSessionManager()
}

// Handlers retrieve singletons via getters
mgr := sshtunnel.GetSSHManager()
tunnelMgr := sshtunnel.GetTunnelManager()
sessionMgr := sshtunnel.GetSessionManager()
```

---

## 2. Package Reference

| Package | Path | Key Types | Responsibility |
|---------|------|-----------|----------------|
| **sshkeys** | `internal/sshkeys/` | (functions only) | ED25519 key generation, storage, rotation, fingerprints, TOFU |
| **sshmanager** | `internal/sshmanager/` | `SSHManager`, `ConnectionMetrics`, `ConnectionState` | Connection pooling, health checks, keepalive, reconnection |
| **sshtunnel** | `internal/sshtunnel/` | `TunnelManager`, `ActiveTunnel`, `TunnelConfig` | SSH port-forwarding, tunnel health, byte counting |
| **sshfiles** | `internal/sshfiles/` | (functions only) | Remote file CRUD via SSH exec (`ls`, `cat`, `mkdir`) |
| **sshlogs** | `internal/sshlogs/` | `StreamOptions` | Log streaming via `tail -F` over SSH exec |
| **sshterminal** | `internal/sshterminal/` | `SessionManager`, `ManagedSession`, `ScrollbackBuffer` | Interactive PTY sessions, scrollback, session recording |
| **sshaudit** | `internal/sshaudit/` | `Auditor`, `AuditEntry`, `QueryOptions` | Security event logging with database persistence |

### Dependency Graph

```
sshtunnel/registry.go
    ├── sshmanager.SSHManager     (connection pool)
    ├── sshtunnel.TunnelManager   (tunnels)
    └── sshterminal.SessionManager (terminal sessions)

sshfiles, sshlogs, sshterminal
    └── use *ssh.Client from sshmanager (passed as parameter)

sshaudit
    └── standalone (uses GORM database, no SSH dependency)

sshkeys
    └── standalone (filesystem + crypto only)
```

---

## 3. Adding New SSH-Based Functionality

### Pattern: Remote Command Execution

The most common extension pattern is running a command on the agent via SSH exec. Follow the pattern established in `sshfiles`:

```go
package myfeature

import (
    "bytes"
    "fmt"

    gossh "golang.org/x/crypto/ssh"
)

// GetAgentUptime retrieves the agent's uptime via SSH.
func GetAgentUptime(client *gossh.Client) (string, error) {
    session, err := client.NewSession()
    if err != nil {
        return "", fmt.Errorf("create session: %w", err)
    }
    defer session.Close()

    var stdout, stderr bytes.Buffer
    session.Stdout = &stdout
    session.Stderr = &stderr

    if err := session.Run("uptime -p"); err != nil {
        return "", fmt.Errorf("run uptime: %s: %w", stderr.String(), err)
    }

    return stdout.String(), nil
}
```

**Key rules:**

1. **Accept `*ssh.Client` as a parameter** -- never create SSH connections directly. The caller obtains the client from `sshmanager`.
2. **Create a new `session` per operation** -- sessions are lightweight SSH channels multiplexed over the single connection.
3. **Always `defer session.Close()`** -- prevents resource leaks.
4. **Shell-quote all user-provided paths** -- use the `shellQuote` pattern from `sshfiles`:

```go
func shellQuote(s string) string {
    return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Usage: safe against injection
cmd := fmt.Sprintf("cat %s", shellQuote(userPath))
```

### Pattern: Writing Data via Stdin

For sending data to the agent (file writes, config updates), pipe through stdin instead of shell arguments to avoid size limits:

```go
func SendConfig(client *gossh.Client, path string, data []byte) error {
    session, err := client.NewSession()
    if err != nil {
        return fmt.Errorf("create session: %w", err)
    }
    defer session.Close()

    session.Stdin = bytes.NewReader(data)

    var stderr bytes.Buffer
    session.Stderr = &stderr

    cmd := fmt.Sprintf("cat > %s", shellQuote(path))
    if err := session.Run(cmd); err != nil {
        return fmt.Errorf("write %s: %s: %w", path, stderr.String(), err)
    }
    return nil
}
```

### Pattern: Streaming Output

For long-running commands (log tailing, process monitoring), use the pattern from `sshlogs`:

```go
func StreamOutput(ctx context.Context, client *gossh.Client, cmd string) (<-chan string, error) {
    session, err := client.NewSession()
    if err != nil {
        return nil, fmt.Errorf("create session: %w", err)
    }

    stdout, err := session.StdoutPipe()
    if err != nil {
        session.Close()
        return nil, fmt.Errorf("stdout pipe: %w", err)
    }

    if err := session.Start(cmd); err != nil {
        session.Close()
        return nil, fmt.Errorf("start command: %w", err)
    }

    ch := make(chan string, 100)
    go func() {
        defer session.Close()
        defer close(ch)
        scanner := bufio.NewScanner(stdout)
        for scanner.Scan() {
            select {
            case <-ctx.Done():
                return
            case ch <- scanner.Text():
            }
        }
    }()

    return ch, nil
}
```

### Wiring Into Handlers

After creating your SSH-based function, wire it into an HTTP handler:

```go
// In internal/handlers/myfeature.go
func (h *Handler) GetAgentUptime(w http.ResponseWriter, r *http.Request) {
    instanceName := chi.URLParam(r, "name")

    // Get the SSH client from the global manager
    sshMgr := sshtunnel.GetSSHManager()
    client, err := sshMgr.GetClient(instanceName)
    if err != nil {
        http.Error(w, "SSH not connected", http.StatusServiceUnavailable)
        return
    }

    uptime, err := myfeature.GetAgentUptime(client)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    json.NewEncoder(w).Encode(map[string]string{"uptime": uptime})
}
```

---

## 4. Adding New Tunnel Types

### Step 1: Define the Service Label

Add a new constant in `sshtunnel/tunnel.go`:

```go
const (
    ServiceVNC     ServiceLabel = "vnc"
    ServiceGateway ServiceLabel = "gateway"
    ServiceCustom  ServiceLabel = "custom"
    ServiceMetrics ServiceLabel = "metrics"  // NEW
)
```

### Step 2: Add a Factory Method

Create a convenience method on `TunnelManager`:

```go
// CreateTunnelForMetrics creates a reverse tunnel from the agent's
// Prometheus metrics port to an auto-assigned local port.
func (tm *TunnelManager) CreateTunnelForMetrics(ctx context.Context, instanceName string) (int, error) {
    const metricsPort = 9090
    return tm.createReverseTunnel(ctx, instanceName, metricsPort, 0, ServiceMetrics)
}
```

The internal `createReverseTunnel` method handles all the plumbing:
- Binds an ephemeral local port (`127.0.0.1:0`)
- Spawns a goroutine that accepts connections and forwards them through the SSH channel
- Registers the tunnel in the manager's state
- Supports byte counting via `countingConn`

### Step 3: Include in Standard Startup (Optional)

If your tunnel should start automatically for every instance, add it to `StartTunnelsForInstance`:

```go
func (tm *TunnelManager) StartTunnelsForInstance(ctx context.Context, instanceName string) error {
    // Existing VNC tunnel
    vncPort, err := tm.CreateTunnelForVNC(ctx, instanceName)
    // ...

    // Existing Gateway tunnel
    gwPort, err := tm.CreateTunnelForGateway(ctx, instanceName, DefaultGatewayPort)
    // ...

    // NEW: Metrics tunnel
    metricsPort, err := tm.CreateTunnelForMetrics(ctx, instanceName)
    if err != nil {
        tm.CloseTunnels(instanceName)
        return fmt.Errorf("create metrics tunnel: %w", err)
    }
    log.Printf("[tunnel] metrics tunnel for %s ready on local port %d", instanceName, metricsPort)

    // Start health monitoring
    // ...
}
```

### Step 4: Expose via Handler

Create a handler that proxies requests to the tunnel's local port:

```go
func (h *Handler) ProxyMetrics(w http.ResponseWriter, r *http.Request) {
    instanceName := chi.URLParam(r, "name")
    tunnelMgr := sshtunnel.GetTunnelManager()

    tunnels := tunnelMgr.GetTunnels(instanceName)
    for _, t := range tunnels {
        if t.Config.Service == sshtunnel.ServiceMetrics {
            target := fmt.Sprintf("http://127.0.0.1:%d%s", t.LocalPort, r.URL.Path)
            // Proxy the request...
            return
        }
    }
    http.Error(w, "metrics tunnel not found", http.StatusNotFound)
}
```

### How Tunnels Work Internally

Each tunnel binds a local TCP listener. When a connection arrives:

```
Browser/Client
    │
    ▼
local listener (127.0.0.1:random_port)
    │
    ▼
client.Dial("tcp", "127.0.0.1:remote_port")  ← SSH channel
    │
    ▼
agent service (e.g., VNC on :3000)
```

Multiple concurrent connections are supported -- each gets its own SSH channel but all share the same underlying SSH connection (multiplexing).

---

## 5. Extending Health Monitoring

### SSH Connection Health

The `SSHManager` runs a keepalive loop every 30 seconds that:
1. Sends an SSH keepalive request
2. Runs `echo ping` via exec with a 5-second timeout
3. On failure: removes the dead client and triggers reconnection with exponential backoff

**Adding custom health checks:**

Register a state change callback during initialization:

```go
mgr := sshtunnel.GetSSHManager()
mgr.OnConnectionStateChange(func(name string, from, to sshmanager.ConnectionState) {
    switch to {
    case sshmanager.StateFailed:
        alerting.Send("SSH connection failed for " + name)
    case sshmanager.StateConnected:
        alerting.Clear("SSH connection failed for " + name)
    }
})
```

**Extending with application-level checks:**

```go
func CustomHealthCheck(mgr *sshmanager.SSHManager, instanceName string) error {
    // Built-in check first
    if err := mgr.HealthCheck(instanceName); err != nil {
        return err
    }

    // Custom: verify agent service is responsive
    client, err := mgr.GetClient(instanceName)
    if err != nil {
        return err
    }

    session, err := client.NewSession()
    if err != nil {
        return fmt.Errorf("create session: %w", err)
    }
    defer session.Close()

    var stdout bytes.Buffer
    session.Stdout = &stdout
    if err := session.Run("systemctl is-active openclaw"); err != nil {
        return fmt.Errorf("openclaw service not active: %w", err)
    }
    return nil
}
```

### Tunnel Health

Tunnels have two tiers of health monitoring:

| Tier | Interval | What It Does |
|------|----------|--------------|
| **Global probe** | 60s | TCP-dials every tunnel's local port; closes unresponsive ones |
| **Per-instance monitor** | 10s | Detects closed tunnels; recreates with exponential backoff |

**Reconnection backoff parameters** (defined in `sshtunnel/tunnel.go`):

```go
reconnectBaseDelay     = 1 * time.Second   // Initial delay
reconnectMaxDelay      = 60 * time.Second  // Cap
reconnectBackoffFactor = 2                  // Multiplier per attempt
```

**Monitoring reconnection metrics:**

```go
tunnelMgr := sshtunnel.GetTunnelManager()
metrics := tunnelMgr.GetTunnelMetrics(instanceName)
for _, m := range metrics {
    log.Printf("tunnel %s:%d healthy=%v bytes=%d",
        m.Service, m.LocalPort, m.Healthy, m.BytesTransferred)
}
```

### Adding New Audit Events

To track a new type of security event:

```go
// 1. Add event type constant in sshaudit/audit.go
const (
    EventVNCSession = "vnc_session"  // NEW
)

// 2. Add helper function in sshaudit/helpers.go
func LogVNCSession(instanceID uint, instanceName, username, sourceIP string) {
    if a := GetAuditor(); a != nil {
        a.Log(AuditEntry{
            InstanceID:   instanceID,
            InstanceName: instanceName,
            EventType:    EventVNCSession,
            Username:     username,
            SourceIP:     sourceIP,
            Details:      "action=start",
        })
    }
}

// 3. Call from handler
sshaudit.LogVNCSession(instance.ID, instance.Name, user, sourceIP)
```

The `GetAuditor()` nil-check pattern makes helpers safe to call even if the audit subsystem is not initialized.

---

## 6. Testing SSH Components

### Test File Organization

Each SSH package follows a consistent test structure:

| File Pattern | Purpose | Requires Infrastructure |
|---|---|---|
| `*_test.go` | Unit tests with mock SSH server | No (in-process) |
| `*_integration_test.go` | Tests with real SSH connections | No (in-process server) |
| `*_pentest_test.go` | Security hardening tests | No |
| `performance_test.go` | Benchmarks | No |

### Running Tests

```bash
# Run all SSH package tests
cd control-plane
go test ./internal/sshkeys/... ./internal/sshmanager/... ./internal/sshtunnel/... \
       ./internal/sshfiles/... ./internal/sshlogs/... ./internal/sshterminal/... \
       ./internal/sshaudit/...

# Run a specific package
go test ./internal/sshfiles/... -v

# Run with race detector (recommended for concurrency tests)
go test ./internal/sshmanager/... -race

# Run benchmarks
go test ./internal/sshtunnel/... -bench=. -benchtime=5s
```

### Building a Test SSH Server

The codebase provides a reusable pattern for creating in-process SSH servers. Here's the pattern from `sshfiles/files_test.go`:

```go
// commandHandler receives a command and returns (stdout, stderr, exitCode)
type commandHandler func(cmd string, stdin io.Reader) (stdout, stderr string, exitCode int)

// startExecSSHServer creates an in-process SSH server for testing.
// Returns an *ssh.Client connected to it and a cleanup function.
func startExecSSHServer(t *testing.T, handler commandHandler) (client *gossh.Client, cleanup func()) {
    t.Helper()

    // Generate server host key
    _, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
    hostSigner, _ := gossh.NewSignerFromKey(hostPriv)

    // Generate client key pair
    clientPub, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
    clientSSHPub, _ := gossh.NewPublicKey(clientPub)

    serverCfg := &gossh.ServerConfig{
        PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
            if bytes.Equal(key.Marshal(), clientSSHPub.Marshal()) {
                return &gossh.Permissions{}, nil
            }
            return nil, fmt.Errorf("unknown public key")
        },
    }
    serverCfg.AddHostKey(hostSigner)

    listener, _ := net.Listen("tcp", "127.0.0.1:0")

    go func() {
        for {
            conn, err := listener.Accept()
            if err != nil {
                return
            }
            go handleSSHConn(conn, serverCfg, handler)
        }
    }()

    clientSigner, _ := gossh.NewSignerFromKey(clientPriv)
    clientCfg := &gossh.ClientConfig{
        User:            "root",
        Auth:            []gossh.AuthMethod{gossh.PublicKeys(clientSigner)},
        HostKeyCallback: gossh.InsecureIgnoreHostKey(),
        Timeout:         5 * time.Second,
    }

    sshClient, _ := gossh.Dial("tcp", listener.Addr().String(), clientCfg)

    return sshClient, func() {
        sshClient.Close()
        listener.Close()
    }
}
```

### Writing Unit Tests

**Pattern: Mock the command handler to control agent responses.**

```go
func TestListDirectory(t *testing.T) {
    // Define what the mock SSH server returns for "ls" commands
    lsOutput := `total 16
drwxr-xr-x  4 root root 4096 Jan 15 10:30 .
drwxr-xr-x 20 root root 4096 Jan 15 09:00 ..
-rw-r--r--  1 root root  220 Jan 15 10:30 .bashrc
drwxr-xr-x  2 root root 4096 Jan 15 10:30 Documents
`
    client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
        if strings.Contains(cmd, "ls -la") {
            return lsOutput, "", 0
        }
        return "", "unknown command", 1
    })
    defer cleanup()

    entries, err := ListDirectory(client, "/root")
    if err != nil {
        t.Fatalf("ListDirectory: %v", err)
    }
    if len(entries) != 2 {
        t.Fatalf("expected 2 entries, got %d", len(entries))
    }
}
```

**Pattern: Test error paths by returning non-zero exit codes.**

```go
func TestReadFileNotFound(t *testing.T) {
    client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
        return "", "cat: /nonexistent: No such file or directory", 1
    })
    defer cleanup()

    _, err := ReadFile(client, "/nonexistent")
    if err == nil {
        t.Fatal("expected error for nonexistent file")
    }
    if !strings.Contains(err.Error(), "No such file or directory") {
        t.Errorf("unexpected error: %v", err)
    }
}
```

**Pattern: Test stdin piping for write operations.**

```go
func TestWriteFile(t *testing.T) {
    var receivedData string
    client, cleanup := startExecSSHServer(t, func(cmd string, stdin io.Reader) (string, string, int) {
        data, _ := io.ReadAll(stdin)
        receivedData = string(data)
        return "", "", 0
    })
    defer cleanup()

    err := WriteFile(client, "/root/test.txt", []byte("hello"))
    if err != nil {
        t.Fatalf("WriteFile: %v", err)
    }
    if receivedData != "hello" {
        t.Errorf("expected 'hello', got %q", receivedData)
    }
}
```

### Writing Integration Tests

For tests that need a full SSH connection lifecycle (connect, health check, reconnect), use the `startTestSSHServer` helper from `sshmanager/manager_test.go`:

```go
func TestConnectAndHealthCheck(t *testing.T) {
    addr, cleanup := startTestSSHServerWithExec(t)
    defer cleanup()

    keyPath := os.Getenv("TEST_SSH_KEY_PATH")
    host, portStr, _ := net.SplitHostPort(addr)
    var port int
    fmt.Sscanf(portStr, "%d", &port)

    m := NewSSHManager(0)
    defer m.CloseAll()

    _, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
    if err != nil {
        t.Fatalf("Connect: %v", err)
    }

    // Health check uses "echo ping" command
    err = m.HealthCheck("test-instance")
    if err != nil {
        t.Fatalf("HealthCheck should pass: %v", err)
    }
}
```

### Testing Reconnection

To test reconnection behavior, use `connTracker` to force-close server connections:

```go
func TestReconnectOnServerDeath(t *testing.T) {
    addr, tracker, cleanup := startTestSSHServerWithConns(t)

    keyPath := os.Getenv("TEST_SSH_KEY_PATH")
    host, portStr, _ := net.SplitHostPort(addr)
    var port int
    fmt.Sscanf(portStr, "%d", &port)

    m := NewSSHManager(0)
    defer m.CloseAll()

    _, err := m.Connect(context.Background(), "test-instance", host, port, keyPath)
    if err != nil {
        t.Fatalf("Connect: %v", err)
    }

    // Kill server and all connections
    cleanup()
    tracker.CloseAll()
    time.Sleep(100 * time.Millisecond)

    // Health check should detect dead connection
    m.checkConnections()

    if m.HasClient("test-instance") {
        t.Error("dead connection should be removed")
    }
}
```

### Testing Concurrency

The SSH packages are heavily concurrent. Always test with the race detector:

```go
func TestConcurrentAccess(t *testing.T) {
    m := NewSSHManager(0)
    defer m.CloseAll()

    var wg sync.WaitGroup
    for i := 0; i < 50; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            name := fmt.Sprintf("instance-%d", i)
            m.SetClient(name, nil)
            m.HasClient(name)
            m.GetConnection(name)
            m.ConnectionCount()
        }(i)
    }
    wg.Wait()
}
```

```bash
go test ./internal/sshmanager/... -race -count=5
```

### Testing Security Properties

Security tests validate hardening constraints (allowed shells, rate limits, input sizes):

```go
func TestAllowedShellsOnly(t *testing.T) {
    // Verify that only approved shells are accepted
    for _, shell := range []string{"/bin/bash", "/bin/sh", "/bin/zsh"} {
        if !isAllowedShell(shell) {
            t.Errorf("expected %s to be allowed", shell)
        }
    }
    for _, shell := range []string{"/bin/evil", "bash; rm -rf /", "../../../etc/passwd"} {
        if isAllowedShell(shell) {
            t.Errorf("expected %s to be rejected", shell)
        }
    }
}
```

---

## 7. Debugging SSH Issues During Development

### Enable Debug Logging

All SSH packages use standardized log prefixes. Filter by prefix to isolate issues:

```bash
# All SSH connection events
go run . 2>&1 | grep "\[ssh\]"

# Key operations
go run . 2>&1 | grep "\[sshkeys\]"

# Tunnel creation and forwarding
go run . 2>&1 | grep "\[tunnel\]"

# Tunnel health checks
go run . 2>&1 | grep "\[tunnel-health\]"

# File operations
go run . 2>&1 | grep "\[sshfiles\]"

# Log streaming
go run . 2>&1 | grep "\[sshlogs\]"

# Terminal sessions
go run . 2>&1 | grep "\[sshterminal\]\|\\[session-mgr\]"

# Audit events
go run . 2>&1 | grep "\[ssh-audit\]"
```

### Log Prefix Reference

| Package | Prefix | What It Logs |
|---------|--------|--------------|
| sshkeys | `[sshkeys]` | Key generation, save/load, rotation steps |
| sshmanager | `[ssh]` | Connect, disconnect, health check, reconnect |
| sshtunnel | `[tunnel]` | Tunnel create, close, forwarding errors |
| sshtunnel | `[tunnel-health]` | Global health probe results |
| sshfiles | `[sshfiles]` | File list/read/write operations |
| sshlogs | `[sshlogs]` | Log stream start/stop |
| sshterminal | `[sshterminal]` | Session create/close, PTY resize |
| sshterminal | `[session-mgr]` | Session manager lifecycle |
| sshaudit | `[ssh-audit]` | All audit events |

### Common Development Issues

**Issue: "no SSH connection for instance X"**

The SSH client for the instance hasn't been established or was cleaned up.

```go
// Check if connected
mgr := sshtunnel.GetSSHManager()
if !mgr.HasClient("my-instance") {
    // Connection doesn't exist -- was it ever created?
    // Check events for history
    events := mgr.GetEvents("my-instance")
    for _, e := range events {
        log.Printf("event: %s at %s: %s", e.Type, e.Timestamp, e.Details)
    }
}
```

**Issue: "SSH session creation fails but connection is alive"**

The SSH connection may be in a degraded state. The keepalive passes (SSH protocol level) but session creation fails (application level).

```bash
# Check health manually
curl http://localhost:8080/api/v1/instances/my-instance/ssh-test
```

**Issue: "Tunnel port not accessible"**

```go
// Get tunnel details
tunnelMgr := sshtunnel.GetTunnelManager()
tunnels := tunnelMgr.GetTunnels("my-instance")
for _, t := range tunnels {
    log.Printf("tunnel: service=%s local=%d remote=%d closed=%v",
        t.Config.Service, t.LocalPort, t.Config.RemotePort, t.IsClosed())
}
```

**Issue: "Key rotation fails mid-way"**

Key rotation is atomic with rollback. If it fails, check the rotation steps:

1. Generate new ED25519 pair
2. Append new public key to agent's `authorized_keys`
3. Test connection with new key
4. Remove old key from `authorized_keys`
5. Update local private key file

If step 3 fails, the old key is still valid and the new key is removed from `authorized_keys`.

### Using the API for Debugging

```bash
# SSH connection status
curl -s http://localhost:8080/api/v1/instances/my-instance/ssh-status | jq .

# Connection events history
curl -s http://localhost:8080/api/v1/instances/my-instance/ssh-events | jq .

# Active tunnels
curl -s http://localhost:8080/api/v1/instances/my-instance/tunnels | jq .

# Force reconnect
curl -s -X POST http://localhost:8080/api/v1/instances/my-instance/ssh-reconnect

# Test connectivity
curl -s http://localhost:8080/api/v1/instances/my-instance/ssh-test | jq .

# Audit logs
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?instance=my-instance&limit=20" | jq .
```

---

## 8. Error Handling Best Practices

### Wrap Errors with Context

All SSH packages use `fmt.Errorf` with `%w` for error chaining. Include the operation name as prefix:

```go
// Good: operation context + wrapped error
return fmt.Errorf("read file %s: %w", path, err)

// Good: validation error with clear message
return nil, fmt.Errorf("connect: host is empty")

// Bad: no context
return err

// Bad: lost error chain
return fmt.Errorf("failed: %s", err.Error())
```

### Check Nil Clients

Always handle the case where `GetClient` returns an error (instance not connected):

```go
client, err := sshMgr.GetClient(instanceName)
if err != nil {
    // Return 503 Service Unavailable, not 500
    http.Error(w, "SSH not connected", http.StatusServiceUnavailable)
    return
}
```

### Handle SSH Session Errors

Sessions can fail even when the connection is alive (e.g., max channels exceeded):

```go
session, err := client.NewSession()
if err != nil {
    // The connection might be dead -- let the health check handle it
    return fmt.Errorf("create SSH session: %w", err)
}
defer session.Close()
```

### Sanitize Log Output

Use `logutil.SanitizeForLog()` for any user-provided data in log messages to prevent log injection:

```go
import "github.com/gluk-w/claworc/control-plane/internal/logutil"

log.Printf("[myfeature] operation on %s", logutil.SanitizeForLog(userInput))
```

### Timeout All Remote Operations

Never run remote commands without a timeout context:

```go
ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
defer cancel()

// Use ctx for any operation that talks to the agent
```

---

## 9. Code Patterns Reference

### Pattern: Safe Nil-Check for Global Singletons

The audit system uses a safe pattern for optional singletons:

```go
func LogConnection(instanceID uint, name, user, ip string) {
    if a := GetAuditor(); a != nil {
        a.Log(AuditEntry{...})
    }
    // Safe to call even if auditor not initialized
}
```

### Pattern: Ring Buffer for Events

The `SSHManager` uses a ring buffer (capped slice) for connection events:

```go
const maxEventsPerInstance = 100

func (m *SSHManager) emitEvent(instanceName string, eventType EventType, details string) {
    m.eventsMu.Lock()
    events := m.events[instanceName]
    events = append(events, event)
    if len(events) > maxEventsPerInstance {
        events = events[len(events)-maxEventsPerInstance:]
    }
    m.events[instanceName] = events
    m.eventsMu.Unlock()
}
```

### Pattern: Connection State Machine

```
disconnected ──► connecting ──► connected ──► reconnecting ──► failed
     ▲                              │               │
     └──────────────────────────────┘               │
     └──────────────────────────────────────────────┘
```

State transitions emit callbacks registered via `OnConnectionStateChange`.

### Pattern: Exponential Backoff for Reconnection

```go
delay := reconnectBaseDelay  // 1s
for attempt := 1; attempt <= maxRetries; attempt++ {
    err := connect(...)
    if err == nil {
        return nil
    }

    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(delay):
    }

    delay *= reconnectBackoffFactor  // 2x
    if delay > reconnectMaxDelay {   // Cap at 60s
        delay = reconnectMaxDelay
    }
}
```

### Pattern: Counting Wrapper for Metrics

Wrap `net.Conn` to track bytes without modifying the tunnel logic:

```go
type countingConn struct {
    net.Conn
    tunnel *ActiveTunnel
}

func (c *countingConn) Read(b []byte) (int, error) {
    n, err := c.Conn.Read(b)
    if n > 0 {
        c.tunnel.addBytesTransferred(int64(n))
    }
    return n, err
}
```

### Pattern: Scrollback Buffer for Session Replay

Terminal sessions use a circular byte buffer for scrollback replay:

```go
type ScrollbackBuffer struct {
    mu     sync.Mutex
    data   []byte           // max 1 MB
    closed bool
    notify chan struct{}     // signals new data arrival
}
```

When a WebSocket reconnects to a detached session, the scrollback buffer is replayed to restore terminal state.

---

## Related Documentation

- [SSH Architecture](../architecture/ssh-connectivity.md) -- system design and security model
- [SSH Configuration Reference](../configuration/ssh-configuration.md) -- environment variables and database schema
- [SSH Operations Runbook](../operations/ssh-operations.md) -- monitoring, troubleshooting, and maintenance
- [SSH Troubleshooting Guide](../troubleshooting/ssh-troubleshooting.md) -- error scenarios and solutions
- [Kubernetes Deployment](../deployment/kubernetes.md) -- PVC, network policies, security contexts
- [Docker Deployment](../deployment/docker.md) -- volumes, networking, agent configuration
