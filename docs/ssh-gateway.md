# Inbound SSH Gateway

The SSH gateway (`control-plane/internal/sshgateway/`) lets users connect to
their OpenClaw instances with a plain SSH client:

```
ssh -i ~/.ssh/claworc_stan.pem -p 2222 stan+my-agent@claworc.example.com
```

Because the SSH protocol has no SNI equivalent, the target instance is
encoded in the SSH login name: `<username>+<instance-name>`. The `bot-`
prefix of stored instance names may be omitted. `scp` and `sftp` work the
same way.

## How it works

1. The control plane listens on `CLAWORC_SSH_GATEWAY_PORT` (default 2222)
   with its own ED25519 host key (`<DATA_PATH>/ssh_gateway_host_key`,
   generated on first boot).
2. **Authentication** is by public key only. Users generate an identity from
   Profile → SSH Access (the server stores only the public key in the
   `user_ssh_keys` table; the private key is downloaded once and never
   stored) or upload their own public key. Keys are matched by SHA256
   fingerprint and verified byte-for-byte. `CLAWORC_AUTH_DISABLED` does not
   affect the gateway — a key is always required.
3. **Authorization** reuses the standard RBAC
   (`database.CanUserAccessInstance`): admins reach every instance, team
   managers their team's instances, regular members instances with an
   explicit `UserInstance` grant. A valid key with a missing/unknown/
   unauthorized instance still authenticates, then receives an explanatory
   message and exit status 1 (unknown and unauthorized are rendered
   identically to prevent name enumeration).
4. **Bridging**: each inbound `session` channel is bridged onto the control
   plane's existing multiplexed SSH connection to the instance
   (`SSHManager.EnsureConnectedWithIPCheck`) — the same transport that
   carries the VNC/LLM/CDP tunnels and web terminals. The gateway never
   dials its own connection and never closes the shared client; a user
   session is just one more channel. Sessions land as `root` on the
   instance, consistent with the existing web terminal and file APIs.

## v1 scope and limits

- `session` channels only: interactive shell, exec, scp, sftp.
- `direct-tcpip`/`tcpip-forward` (port forwarding) are rejected; the agent
  sshd's `PermitOpen` allowlist would block arbitrary targets anyway.
- The agent sshd's `MaxSessions` (OpenSSH default 10) caps concurrent
  channels per instance, shared with web terminals and tunnels.
- Brute-force protection: 30s handshake timeout, `MaxAuthTries` 3, per-IP
  ban (10 failed auths within 1 minute → 5 minute ban), 64 concurrent
  connection cap.

## Configuration

| Env var | Default | Meaning |
|---|---|---|
| `CLAWORC_SSH_GATEWAY_ENABLED` | `true` | Enable the listener |
| `CLAWORC_SSH_GATEWAY_PORT` | `2222` | Listen port |
| `CLAWORC_SSH_GATEWAY_PUBLIC_HOST` | (empty) | Display-only hostname for the UI snippet; empty = browser hostname |

Helm: `sshGateway.{enabled,port,servicePort,nodePort,publicHost}` in
`helm/values.yaml` (adds the container port, env vars, and a Service port).
Exposure beyond the cluster (LoadBalancer, ingress TCP passthrough) is
environment-specific.

## API

Authenticated endpoints under `/api/v1`:

- `POST /auth/ssh-keys/generate` → `{key, private_key}` (private key
  returned exactly once)
- `POST /auth/ssh-keys` `{name?, public_key}` — upload own key (409 on
  duplicate fingerprint)
- `GET /auth/ssh-keys` — list (fingerprints + metadata only)
- `DELETE /auth/ssh-keys/{keyId}` — revoke (owner-scoped)
- `GET /ssh-gateway/info` → `{enabled, port, host}`

## Audit events

Recorded in `ssh_audit_logs` (see `internal/sshaudit/`):

- `gateway_login` — successful key auth (details include remote IP,
  fingerprint, requested instance, deny reason if any)
- `gateway_login_failed` — failed auth attempt
- `gateway_session` — shell/exec/subsystem started (exec commands truncated)
- `gateway_disconnection` — connection closed
- `key_upload` / `key_rotation` — user key generated/uploaded / revoked
