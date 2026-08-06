# Channel Health Monitoring

## Overview

The control plane monitors whether each OpenClaw instance's chat channels
(Slack, Telegram, Discord, WhatsApp, etc.) are alive and receiving events.
Every 60 seconds (configurable) it connects to the instance's OpenClaw
gateway over the existing SSH tunnel, calls the gateway's `channels.status`
API, evaluates per-channel health, and persists the latest result. The UI
surfaces this as a **Channel Health** panel on the Agent detail page and a
warning indicator in the agent list.

Monitoring only observes. It never restarts channels or delivers alerts —
see [Future work](#future-work).

## Architecture

```
poller (every CLAWORC_CHANNEL_HEALTH_INTERVAL, default 60s)
  → OpenClaw gateway over the existing SSH tunnel (WS RPC `channels.status`)
  → health evaluation (per-channel + overall)
  → persistence (`channel_health_statuses` table) + in-memory snapshot
  → API (`/api/v1/instances/{id}/channels/health`, instance-list summary)
  → UI (Channel Health panel, agent-list warning indicator)
```

1. A background poller ticks once per interval and checks every running
   instance. Checks ride the control plane's existing multiplexed SSH
   connection to the instance — no new connections are dialed.
2. Each check calls the OpenClaw gateway's `channels.status` WebSocket RPC,
   which reports every channel known to the OpenClaw config along with its
   connection state, last-event time, last error, and reconnect count.
3. The evaluator maps the raw gateway report onto the health states below
   and computes the instance-level overall status.
4. The latest per-channel status is written to the
   `channel_health_statuses` table and kept in an in-memory snapshot; the
   API serves from the snapshot and falls back to the persisted rows after
   a control-plane restart.

## Health states

Per channel:

| State | Meaning |
|---|---|
| `healthy` | Channel connected and receiving events |
| `stale` | Socket connected but no events for over **30 minutes**. Applies only to persistent-socket modes (e.g. Slack Socket Mode); webhook/http modes are exempt |
| `disconnected` | Channel running but its connection to the provider is down |
| `not_running` | Channel enabled in the OpenClaw config but not running |
| `disabled` | Channel disabled/unconfigured in the OpenClaw config |

Instance level:

| State | Meaning |
|---|---|
| `unreachable` | Gateway not responding — the OpenClaw process may be down |
| `no_channels` | Gateway reachable but no channels configured |
| `unknown` | Not yet checked |

Overall instance status is derived from the per-channel states:
**unhealthy** if any channel is `disconnected` or `not_running`,
**degraded** if any channel is `stale`, **healthy** otherwise.

## Configuration

| Env var | Default | Meaning |
|---|---|---|
| `CLAWORC_CHANNEL_HEALTH_ENABLED` | `true` | Enable the poller (and with it the escalation pipeline) |
| `CLAWORC_CHANNEL_HEALTH_INTERVAL` | `60s` | Time between checks |
| `CLAWORC_CHANNEL_HEALTH_ALERT_THRESHOLD` | `3` | Consecutive failing checks before an alert fires |
| `CLAWORC_CHANNEL_HEALTH_RESTART_THRESHOLD` | `5` | Consecutive failing checks before an auto-restart fires |
| `CLAWORC_CHANNEL_HEALTH_RESTART_MAX_PER_HOUR` | `3` | Auto-restart circuit breaker (per instance, rolling hour) |
| `CLAWORC_CHANNEL_HEALTH_RESTART_COOLDOWN` | `10m` | After a triggered restart, failing checks are ignored for this long |

Runtime behavior (UI-editable, stored in the settings table):

| Setting key | Default | Meaning |
|---|---|---|
| `channel_alerts_enabled` | `true` | Deliver webhook alerts (inert without a URL) |
| `channel_alert_webhook_url` | empty | Where alert JSON is POSTed |
| `channel_alert_webhook_token` | empty | Optional `Authorization: Bearer` token (encrypted at rest) |
| `channel_auto_restart_enabled` | `false` | Opt-in automatic instance restarts |

## Escalation

The monitor feeds every snapshot to an escalator (`internal/handlers/channel_escalation.go`)
that tracks consecutive failing checks per instance. "Failing" means overall
`unhealthy` or `unreachable`; `degraded`/`unknown` *hold* an open incident
(neither count nor reset it); `healthy`/`no_channels` close it.

Escalation ladder:

1. **Alert** — at the alert threshold, one `channel_failure` webhook fires
   per incident.
2. **Auto-restart** (opt-in) — at the restart threshold the instance is
   restarted through the same async flow as a manual restart (tunnels
   stopped, task + toast emitted). Guarded by the per-hour circuit breaker
   and the post-restart cooldown; when the breaker trips, a single
   `restart_limit_reached` webhook asks for manual intervention.
3. **Recovery** — when the incident closes after an alert was sent, one
   `recovery` webhook reports the outage duration.

Alert payloads are JSON with a human-readable `text` field plus structured
fields (`event`, `instance`, `overall`, `consecutive_failures`,
`failing_since`, `channels[]`). Delivery is fire-and-forget with one retry
on network error or 5xx. Admins can verify delivery with
`POST /api/v1/settings/channel-alerts/test` (the "Send Test" button in
Settings → Misc).

Every escalation action is recorded in the `channel_health_events` audit
table and readable via `GET /api/v1/instances/{id}/channels/health/events`.
Incident counters are in-memory: a control-plane restart re-counts an
ongoing outage from zero (worst case, a duplicate alert after ~3 checks).

## API

`GET /api/v1/instances/{id}/channels/health` returns the overall status
plus per-channel detail:

```json
{
  "overall": "degraded",
  "checked_at": "2026-08-06T10:15:00Z",
  "channels": [
    {
      "channel": "slack",
      "status": "healthy",
      "last_event_at": "2026-08-06T10:14:12Z",
      "reconnect_count": 2,
      "error": ""
    },
    {
      "channel": "telegram",
      "status": "stale",
      "last_event_at": "2026-08-06T09:30:00Z",
      "reconnect_count": 0,
      "error": ""
    }
  ]
}
```

Instance list responses include a compact `channel_health` summary so the
agent list can render a warning indicator without an extra request:

```json
{ "overall": "unhealthy", "unhealthy_count": 1, "checked_at": "2026-08-06T10:15:00Z" }
```

## Data model

`channel_health_statuses` stores the latest status per instance/channel
pair (one row per channel, overwritten on each check):

| Field | Type | Description |
|---|---|---|
| `InstanceID` | uint | Instance the channel belongs to |
| `Channel` | string | Channel name (`slack`, `telegram`, …) |
| `Status` | string | One of the per-channel states above |
| `LastEventAt` | datetime | When the channel last received an event |
| `ReconnectCount` | int | Reconnects reported by the gateway |
| `Error` | string | Last error reported by the gateway, if any |
| `CheckedAt` | datetime | When this status was recorded |

`channel_health_events` is the append-only escalation audit log:

| Field | Type | Description |
|---|---|---|
| `InstanceID` | uint | Instance the event belongs to |
| `Type` | string | `failure_detected`, `auto_restart`, `restart_limit_reached`, `recovered`, `webhook_test` |
| `Overall` | string | Overall status at the time of the event |
| `Detail` | text | JSON context (failing channels, consecutive count, outage duration) |
| `WebhookStatus` | string | `sent`, `failed`, or `skipped` |
| `CreatedAt` | datetime | When the event occurred |

## UI

- **Channel Health panel** on the Agent detail page (Settings tab):
  per-channel status badges, last-event times, errors, and reconnect
  counts.
- **Agent list**: agents whose overall status is unhealthy show a warning
  indicator.

## Known limitations

- The gateway's channel state is in-memory OpenClaw state; it resets when
  the gateway restarts, so last-event times and reconnect counts start
  over.
- Staleness is inferred from event silence, so a genuinely quiet channel
  (nobody messaging the agent for 30+ minutes) can be reported `stale`
  even though it is fine.
- Escalation counters are in-memory only; a control-plane restart resets
  consecutive-failure counts and the restart circuit-breaker window.

## Future work

- Synthetic canary probes to distinguish quiet channels from stale ones.
- Consuming the gateway's push `health` broadcast instead of polling.
