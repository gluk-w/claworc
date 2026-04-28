# Analytics (Engineering Reference)

Opt-in, anonymous product telemetry. Events are sent from each Claworc
deployment to a Cloudflare Worker at `https://claworc.com/stats/collect`. No
PII, no message content, no API keys, no env-var values, no folder paths, no
display names — only counts, IDs, and a few numeric/categorical attributes.

## Identity

A 32-char hex `installation_id` is auto-generated on first read and stored in
the `settings` table. It is never reset and never re-rolled. It is the only
identifier sent with events.

## Consent

Stored in `settings.analytics_consent` with three values:

- `unset` — default; the frontend shows the consent modal.
- `opt_in` — `Track()` sends events.
- `opt_out` — `Track()` short-circuits.

The consent state machine is one-shot: once a user picks in or out, the
modal never reappears, and the toggle in **Settings → Anonymous Analytics**
flips between `opt_in` and `opt_out` only.

## API

```go
analytics.Track(ctx, analytics.EventInstanceCreated, map[string]any{
    "total_instances": 7,
})
```

`Track()` is fire-and-forget. It checks consent on every call, marshals a
payload, then dispatches a goroutine bounded by a 5-second timeout. All
errors are swallowed. Unknown event names are dropped.

The `opt_out` event is sent via `TrackForceOptOut()` which bypasses the
consent gate so we can record the toggle-off transition itself; otherwise
`Track()` would short-circuit because the new state is `opt_out`. The send
happens *before* the DB write, not after.

## Wire format

```json
{
  "installation_id": "<32-hex>",
  "event": "<event_name>",
  "props": { "...": "..." },
  "ts": 1700000000,
  "version": "<build version>"
}
```

The Cloudflare Worker validates installation ID format (32 hex), enforces an
event allowlist, then writes the row to the `claworc_events` Analytics
Engine dataset. Indexes: `installation_id`. Blobs: `event`, `version`,
serialized `props`. Doubles: `ts`.

## Event catalog

| Event | Trigger | Props |
|---|---|---|
| `instance_created` | After `DB.Create(&inst)` in `CreateInstance` | `total_instances`, `instance_id`, `provider_aliases`, `cpu_limit`, `memory_limit`, `model`, `provider_name` |
| `instance_deleted` | After `DB.Delete(&inst)` in `DeleteInstance` | `remaining_instances` |
| `skill_uploaded` | After `DB.Create(&skill)` in skill upload handler | `total_skills` |
| `skill_deleted` | After `DB.Delete(&skill)` in `DeleteSkill` | `remaining_skills` |
| `shared_folder_created` | After `database.CreateSharedFolder` | `total_folders`, `agents_shared_with` |
| `shared_folder_deleted` | After `database.DeleteSharedFolder` | `remaining_folders` |
| `backup_schedule_created` | After `database.CreateBackupSchedule` | `cron`, `instances_count` (-1 = all), `retention_days`, `within_data_dir`, `paths_count` |
| `backup_created_manual` | After `backup.CreateFullBackup` | `paths_count` |
| `user_created` | After `database.CreateUser` | `total_users`, `user_id`, `role`, `assigned_instances` |
| `user_updated` | After `UpdateUserPermissions` and `UpdateUserRole` | `total_users`, `user_id`, `role`, `assigned_instances` |
| `user_deleted` | After `database.DeleteUser` | `remaining_users` |
| `password_changed` | After `database.UpdateUserPassword` | `user_id` |
| `provider_added` | After `DB.Create(&provider)` | `provider_alias`, `total_providers` |
| `provider_deleted` | After `DB.Delete(&provider)` | `remaining_providers` |
| `ssh_key_rotated` | After `RotateGlobalKeyPair` succeeds | none |
| `global_env_vars_edited` | After settings env vars actually change | `total_env_vars` |
| `instance_env_vars_edited` | After per-instance env vars actually change | `instance_id`, `local_env_vars`, `global_env_vars` |
| `opt_out` | When the user flips consent from in to out | none |

## What is *not* collected

- API keys, gateway tokens, encrypted secrets
- Env-var names or values (only counts)
- Instance display names, container images, prompts, conversation content
- File paths beyond `within_data_dir: bool`
- Session contents, terminal output, browser activity

## Storage choice

Cloudflare Analytics Engine, dataset `claworc_events`. Cheap, queryable from
Workers and Grafana, indexed by installation ID. Switching to D1 or R2 would
be a one-binding change in the worker.
