# Environment Variables

## Overview

OpenClaw instances run inside containers managed by the Claworc control plane. The
control plane sets a fixed set of internal env vars at container start (gateway
credentials, instance identity, initial model/provider config) and, in addition,
injects two layers of admin-defined env vars:

1. **Global env vars** — admin-defined, applied to every instance.
2. **Per-instance env vars** — override globals on a per-instance basis when the
   name matches.

Skills can declare the env var names they depend on via a
`required_env_vars:` list in their `SKILL.md` frontmatter. At deploy time the
control plane warns (but does not block) if any required name is not satisfied
by either the instance's own env vars or the global defaults.

## Precedence

Values merge in this order at container-create time (highest wins):

1. **System env vars** — reserved names set by the control plane; cannot be
   shadowed.
2. **Per-instance env vars** — the current instance's overrides.
3. **Global env vars** — the admin-defined defaults from settings.

Example: if `MY_TOKEN=global-value` is set globally and the instance defines
`MY_TOKEN=instance-value`, the container sees `MY_TOKEN=instance-value`. If the
admin tries to set `CLAWORC_INSTANCE_ID` as either a global or per-instance
override, the API rejects the request with HTTP 400.

## Reserved names

Only these four exact names are reserved for internal use and rejected on input:

| Name | Purpose |
|------|---------|
| `OPENCLAW_GATEWAY_TOKEN` | Auth token used by OpenClaw to call the LLM gateway. |
| `CLAWORC_INSTANCE_ID` | Numeric DB id of this instance, surfaced to OpenClaw. |
| `OPENCLAW_INITIAL_MODELS` | JSON seed of primary/fallback models, applied before first run. |
| `OPENCLAW_INITIAL_PROVIDERS` | JSON seed of providers (base URL, api_type, virtual key, models). |

All other `OPENCLAW_*` and `CLAWORC_*` names are allowed — users often need to
configure OpenClaw itself or related tooling through env vars that share those
prefixes (e.g. `OPENCLAW_API_URL`, `CLAWORC_CUSTOM_FLAG`).

The reserved list lives in exactly one place: `ReservedEnvVarNames` in
`control-plane/internal/handlers/envvars.go`. The system-env-var injection in
`CreateInstance` / `buildCreateParams` must iterate the same list so the two
stay in sync.

Name format (enforced server-side and client-side): `^[A-Z_][A-Z0-9_]*$`.

## Storage & encryption

Both layers share the same JSON shape:

```json
{"KEY": "<fernet-encrypted-value>", ...}
```

- **Global**: stored in the `settings` table under the `default_env_vars` key
  (seeded with `{}` if absent).
- **Per-instance**: stored on the `instances` table in the `env_vars` TEXT
  column (defaults to `{}`).

Values are **Fernet-encrypted at rest** using the same key and helpers as the
Brave API key (`utils.Encrypt` / `utils.Decrypt`, key auto-generated and stored
in `settings.fernet_key` on first run). API responses decrypt and return
plaintext values via `EnvVarsForResponse` — the settings and instance-detail
endpoints are admin-only, and the edit UI needs the live values to diff
against. Values are decrypted both at container-create time (to inject into
the env block) and at admin read time (to render in the editor).

## Lifecycle

Env vars are injected into the container's env block when the container is
created. Changes to env vars on an already-running instance are **not** hot
applied — the container must be recreated for them to take effect.

To keep the DB state and the live containers in sync, the control plane
**auto-restarts** affected running instances whenever env vars change:

- **Per-instance edit** — `PUT /api/v1/instances/:id` with `env_vars_set` /
  `env_vars_unset` restarts that instance if `status == "running"`. The
  response includes `restarting: true` (and `requires_restart: true` for
  backwards compatibility). Internally the handler calls
  `restartInstanceAsync(inst)` — the same helper used by the shared-folder
  code path in `control-plane/internal/handlers/shared_folders.go`.

- **Global edit** — `PUT /api/v1/settings` with the same fields enumerates
  every `running` instance and fires `restartInstanceAsync` for each. The
  response includes `restarting_instances: [{id, name, display_name}]` so the
  client knows who it's waiting on.

On the frontend, each affected instance gets a persistent loading toast
*"Restarting <display_name> — Setting environment variables"* (shared helper
`envVarRestartToast` in `src/utils/toast.ts`, stable `toastId =
env-restart-<id>`). The existing `useRestartedToast` hook dismisses the loading
toast when it observes the instance transition from `restarting` back to
`running`, and fires the standard `Instance restarted` success toast.

Instances in `status == "stopped"` are left alone — they'll pick up the new
env vars next time the admin starts them.

### Visibility inside the container

The orchestrator writes env vars onto the container's PID 1 environ
(`docker run -e` / Kubernetes `containers[].env`). Three paths lead
into user-visible places inside the container:

- **S6 services** — every `run` script uses `#!/command/with-contenv
  bash`, which re-exports vars captured by s6-overlay's `/init` into
  `/run/s6/container_environment/`. Services like `svc-openclaw` and
  `svc-desktop` therefore see the vars directly.
- **`docker exec` / `kubectl exec`** — inherit PID 1's environ from
  the container runtime.
- **SSH sessions** — do **not** inherit sshd's environ. sshd runs the
  user through PAM and `login.defs`, which build a fresh env. To cover
  this path, the init-setup oneshot
  (`agent/rootfs/etc/s6-overlay/scripts/init-setup.sh`) snapshots PID
  1's env into two files at boot:
  - `/etc/environment` — read by `pam_env.so`, present in
    `/etc/pam.d/sshd`, `cron`, `login`, and `su`.
  - `/etc/profile.d/claworc-env.sh` — sourced by every bash login
    shell (belt-and-suspenders for PAM configurations that skip
    `pam_env`).

  Vars owned by the login shell / PAM / `getpw` (`PATH`, `HOME`,
  `HOSTNAME`, `USER`, `LOGNAME`, …) are excluded from the snapshot so
  they aren't clobbered on login.

  Caveat: pam_env's `/etc/environment` parser treats `#` as a comment
  marker even inside double-quoted values, so a variable whose value
  contains `#` survives **only** through the `/etc/profile.d/claworc-env.sh`
  path — i.e. interactive login shells (including the dashboard's
  terminal tab, which is what users normally hit). Non-interactive
  `ssh host command` invocations skip profile.d and therefore see the
  truncated value. This is an upstream pam_env limitation, not a bug in
  init-setup; the profile.d fallback is precisely there to work around
  it for the primary user-facing path.

The snapshot is orchestrator-independent — the agent image is
byte-identical under Docker and Kubernetes, and `printenv` inside the
container sees whatever the runtime placed on PID 1 regardless of
source (`env:`, `envFrom`, `valueFrom`).

## API

All env var state is surfaced through the existing settings and instances
endpoints — no new routes. Requests are PATCH-style to avoid leaking whole
plaintext maps through responses.

### `GET /api/v1/settings`

Includes the plaintext global env vars (admin-only endpoint):

```json
{
  "...": "...other settings...",
  "default_env_vars": {
    "SLACK_TOKEN": "xoxb-...",
    "TEAM_NAME": "engineering"
  }
}
```

### `PUT /api/v1/settings`

Accepts two new fields:

```json
{
  "env_vars_set": {"SLACK_TOKEN": "xoxb-new-plaintext"},
  "env_vars_unset": ["OLD_KEY"]
}
```

- `env_vars_set` — upsert: each value is encrypted and written into the stored
  map.
- `env_vars_unset` — delete: each name is removed.

Names are validated against the regex and reserved list before any write. A
single invalid or reserved name fails the whole request (HTTP 400).

### `GET /api/v1/instances/:id`

Includes:

```json
{
  "...": "...other instance fields...",
  "env_vars": {"MY_TOKEN": "plaintext-value"},
  "has_env_override": true
}
```

`has_env_override` is a convenience boolean (`env_vars` is non-empty).

### `POST /api/v1/instances`

Accepts:

```json
{
  "display_name": "Bot Alpha",
  "env_vars_set": {"MY_TOKEN": "plaintext"}
}
```

### `PUT /api/v1/instances/:id`

Accepts both `env_vars_set` and `env_vars_unset`, same semantics as on
settings. Response:

```json
{
  "...": "...instance fields...",
  "env_vars": {"MY_TOKEN": "plaintext-value"},
  "has_env_override": true,
  "requires_restart": true,
  "restarting": true
}
```

## Skill integration

Skills declare the env var names they need via their `SKILL.md` frontmatter:

```yaml
---
name: github-deploy-watch
description: Watches GitHub Actions for the current repo and alerts on failures.
required_env_vars:
  - GITHUB_TOKEN
  - GITHUB_OWNER
---
```

At **upload** time, the frontmatter list is persisted on the `Skill` row in a
`required_env_vars` TEXT column (JSON `[]string`). The skill list API
(`GET /api/v1/skills`) surfaces it verbatim.

At **deploy** time, `POST /api/v1/skills/:slug/deploy` computes, per target
instance, the set of required names that are satisfied by neither the global
env vars nor the instance-specific env vars and returns them as
`missing_env_vars` on each `DeployResult`:

```json
{
  "results": [
    {"instance_id": 7, "status": "ok"},
    {"instance_id": 9, "status": "ok", "missing_env_vars": ["GITHUB_TOKEN"]}
  ]
}
```

Missing env vars are a **warning**, not a failure — the skill files are still
copied to the instance. The frontend `DeployModal` fetches the `settings` and
instance data, precomputes the same diff for the user's selection, and shows a
pre-deploy warning banner when any selected instance lacks a required name.

## Source map

| File | Responsibility |
|------|----------------|
| `control-plane/internal/handlers/envvars.go` | `ReservedEnvVarNames`, `ValidateEnvVarName`, encrypt/decrypt map helpers, `LoadGlobalEnvVars`, `LoadInstanceEnvVars`, `MergeUserEnvVars`, `ApplyEnvVarsDelta`, `EnvVarsForResponse`. |
| `control-plane/internal/handlers/envvars_test.go` | Unit tests for validation, round-trip, and merge precedence. |
| `control-plane/internal/handlers/settings.go` | `default_env_vars` in GET; `env_vars_set` / `env_vars_unset` parsing in PUT. |
| `control-plane/internal/handlers/instances.go` | Request/response env var fields; merge in `CreateInstance` and `buildCreateParams`; PATCH-style update in `UpdateInstance`; `requires_restart` hint. |
| `control-plane/internal/handlers/skills.go` | `required_env_vars` frontmatter parsing; persistence on `Skill` row; `missing_env_vars` in deploy results. |
| `control-plane/internal/database/models.go` | `Instance.EnvVars`, `Skill.RequiredEnvVars` columns. |
| `control-plane/internal/database/database.go` | `default_env_vars` seed (`{}`). |
| `control-plane/internal/orchestrator/kubernetes.go`, `docker.go` | Already iterate `CreateParams.EnvVars`; unchanged by this feature. |
| `control-plane/frontend/src/components/KeyValueListEditor.tsx` | Reusable add/edit/delete UI for name/value pairs. |
| `control-plane/frontend/src/pages/SettingsPage.tsx` | Global env vars card, wired to `pendingEnvSet` / `pendingEnvUnset` flushed on Save. |
| `control-plane/frontend/src/components/InstanceForm.tsx` | Per-instance env vars card at create time. |
| `control-plane/frontend/src/pages/InstanceDetailPage.tsx` | Per-instance env vars card at edit time; restart-required info toast. |
| `control-plane/frontend/src/components/skills/DeployModal.tsx` | Pre-deploy and post-deploy missing-env-var warnings. |
