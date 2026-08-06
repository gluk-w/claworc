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
| `CLAWORC_CHANNEL_HEALTH_ENABLED` | `true` | Enable the poller |
| `CLAWORC_CHANNEL_HEALTH_INTERVAL` | `60s` | Time between checks |

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
- Monitoring only observes — it does not restart channels, restart
  instances, or deliver alerts.

## Future work

- Auto-restart escalation for unhealthy channels.
- Alert delivery (notify operators when an agent goes unhealthy).
- Synthetic canary probes to distinguish quiet channels from stale ones.
- Consuming the gateway's push `health` broadcast instead of polling.
