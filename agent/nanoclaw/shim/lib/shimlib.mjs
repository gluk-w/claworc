// shimlib.mjs — shared helpers for the NanoClaw Claworc shim (docs/shim.md).
//
// Runs under Bun (bun:sqlite is the same SQLite driver the NanoClaw
// agent-runner itself uses — no extra dependency). The shim plays the role of
// the upstream NanoClaw *host* process: it owns inbound.db (writes user
// messages, reads nothing the container owns) and reads outbound.db
// (messages_out + processing_ack, written by the agent-runner). The
// single-writer-per-file rule from NanoClaw's docs/db.md is preserved.
import { Database } from 'bun:sqlite';
import fs from 'node:fs';
import path from 'node:path';

// Schemas are imported from the pinned NanoClaw source tree so they can never
// drift from the agent-runner's expectations across NANOCLAW_VERSION bumps.
import { INBOUND_SCHEMA, OUTBOUND_SCHEMA } from '/opt/nanoclaw/src/db/schema.ts';

export const STATE_DIR = process.env.CLAWORC_SHIM_STATE_DIR || '/home/claworc/.claworc/shim/nanoclaw';
export const RUN_DIR = process.env.CLAWORC_SHIM_RUN_DIR || '/run/claworc/shim';
export const SESSIONS_DIR = path.join(STATE_DIR, 'sessions');
// Real, user-visible agent workspace. /workspace/agent symlinks here so the
// unpatched upstream paths in CLAUDE.md / config.ts keep working.
export const AGENT_DIR = process.env.CLAWORC_NANOCLAW_AGENT_DIR || '/home/claworc/workspace';
export const LLM_CONFIG_PATH = path.join(STATE_DIR, 'llm.json');

// Opaque Claworc session keys become file names — same sanitizer as the
// template shim (tr -c 'A-Za-z0-9._-' '_').
export function sanitizeKey(key) {
  return key.replace(/[^A-Za-z0-9._-]/g, '_');
}

export function sessionDir(key) {
  return path.join(SESSIONS_DIR, sanitizeKey(key));
}

export function runnerPidFile(key) {
  return path.join(RUN_DIR, `runner-${sanitizeKey(key)}.pid`);
}

export function chatPidFile(key) {
  return path.join(RUN_DIR, `chat-${sanitizeKey(key)}.pid`);
}

export const HOST_HEARTBEAT = path.join(RUN_DIR, 'host.heartbeat');

export function pidAlive(pid) {
  if (!pid || !Number.isFinite(pid)) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

export function readPidFile(file) {
  try {
    return parseInt(fs.readFileSync(file, 'utf8').trim(), 10);
  } catch {
    return null;
  }
}

export function mtimeAgeMs(file) {
  try {
    return Date.now() - fs.statSync(file).mtimeMs;
  } catch {
    return Infinity;
  }
}

// --- session DB pair -------------------------------------------------------

/**
 * Ensure the session directory exists with both DB files at current schema,
 * plus the destination map and routing row the agent-runner expects the host
 * to have written. Idempotent (CREATE IF NOT EXISTS / upserts).
 */
export function ensureSession(key) {
  const dir = sessionDir(key);
  fs.mkdirSync(dir, { recursive: true });

  const inbound = new Database(path.join(dir, 'inbound.db'));
  try {
    // NanoClaw's cross-mount invariant (journal DELETE, not WAL) is kept even
    // though everything is a local FS here — the runner sets the same pragma.
    inbound.exec('PRAGMA journal_mode = DELETE');
    inbound.exec('PRAGMA busy_timeout = 5000');
    inbound.exec(INBOUND_SCHEMA);
    // Destination map: one channel called "user" — the Claworc chat surface.
    // The runtime system prompt lists it, so the agent addresses replies with
    // <message to="user"> and mcp send_message(to="user").
    inbound
      .prepare(
        `INSERT INTO destinations (name, display_name, type, channel_type, platform_id, agent_group_id)
         VALUES ('user', 'User', 'channel', 'claworc', $pid, NULL)
         ON CONFLICT(name) DO UPDATE SET platform_id = excluded.platform_id`,
      )
      .run({ $pid: key });
    inbound
      .prepare(
        `INSERT INTO session_routing (id, channel_type, platform_id, thread_id)
         VALUES (1, 'claworc', $pid, NULL)
         ON CONFLICT(id) DO UPDATE SET channel_type = excluded.channel_type,
           platform_id = excluded.platform_id, thread_id = excluded.thread_id`,
      )
      .run({ $pid: key });
  } finally {
    inbound.close();
  }

  const outbound = new Database(path.join(dir, 'outbound.db'));
  try {
    outbound.exec('PRAGMA journal_mode = DELETE');
    outbound.exec('PRAGMA busy_timeout = 5000');
    outbound.exec(OUTBOUND_SCHEMA);
  } finally {
    outbound.close();
  }
  return dir;
}

export function openInboundRw(dir) {
  const db = new Database(path.join(dir, 'inbound.db'));
  db.exec('PRAGMA journal_mode = DELETE');
  db.exec('PRAGMA busy_timeout = 5000');
  return db;
}

export function openInboundRo(dir) {
  const db = new Database(path.join(dir, 'inbound.db'), { readonly: true });
  db.exec('PRAGMA busy_timeout = 5000');
  db.exec('PRAGMA mmap_size = 0');
  return db;
}

export function openOutboundRo(dir) {
  const db = new Database(path.join(dir, 'outbound.db'), { readonly: true });
  db.exec('PRAGMA busy_timeout = 5000');
  db.exec('PRAGMA mmap_size = 0');
  return db;
}

/** Host-side even seq (NanoClaw invariant: host even, container odd). */
export function nextEvenSeq(db) {
  const { m } = db.prepare('SELECT COALESCE(MAX(seq), 0) AS m FROM messages_in').get();
  return m < 2 ? 2 : m + 2 - (m % 2);
}

export function generateId(prefix = 'msg') {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

/** Insert one user chat message; returns the row id. */
export function insertUserMessage(dir, key, text) {
  const db = openInboundRw(dir);
  try {
    const id = generateId();
    db.prepare(
      `INSERT INTO messages_in (id, seq, kind, timestamp, status, trigger, platform_id, channel_type, thread_id, content)
       VALUES ($id, $seq, 'chat', $ts, 'pending', 1, $pid, 'claworc', NULL, $content)`,
    ).run({
      $id: id,
      $seq: nextEvenSeq(db),
      $ts: new Date().toISOString(),
      $pid: key,
      $content: JSON.stringify({ sender: 'User', senderId: 'claworc-user', text, isFromMe: false }),
    });
    return id;
  } finally {
    db.close();
  }
}

/**
 * Mark inbound rows terminally handled so a (re)spawned runner never
 * re-processes them (used on abort). We own inbound.db, so this is the
 * host-legal way to retire rows — the container only reads this file.
 */
export function retireInboundMessages(dir, ids) {
  if (!ids.length) return;
  const db = openInboundRw(dir);
  try {
    const stmt = db.prepare(`UPDATE messages_in SET status = 'completed' WHERE id = ?`);
    for (const id of ids) stmt.run(id);
  } finally {
    db.close();
  }
}

/** processing_ack status for a message id: null | processing | completed | failed | script-skip:error */
export function ackStatus(dir, id) {
  let db;
  try {
    db = openOutboundRo(dir);
  } catch {
    return null;
  }
  try {
    const row = db.prepare('SELECT status FROM processing_ack WHERE message_id = ?').get(id);
    return row ? row.status : null;
  } catch {
    return null;
  } finally {
    db.close();
  }
}

/** All messages_out rows with seq > afterSeq, ordered. */
export function outboundRowsAfter(dir, afterSeq) {
  let db;
  try {
    db = openOutboundRo(dir);
  } catch {
    return [];
  }
  try {
    return db
      .prepare('SELECT id, seq, kind, content FROM messages_out WHERE seq > ? ORDER BY seq ASC')
      .all(afterSeq);
  } catch {
    return [];
  } finally {
    db.close();
  }
}

export function maxOutboundSeq(dir) {
  let db;
  try {
    db = openOutboundRo(dir);
  } catch {
    return 0;
  }
  try {
    const { m } = db.prepare('SELECT COALESCE(MAX(seq), 0) AS m FROM messages_out').get();
    return m;
  } catch {
    return 0;
  } finally {
    db.close();
  }
}

/** Extract user-facing text from a messages_out content JSON (chat kinds). */
export function outboundText(row) {
  if (row.kind !== 'chat' && row.kind !== 'chat-sdk') return null;
  try {
    const c = JSON.parse(row.content);
    const t = c.text ?? c.markdown ?? c.fallbackText;
    return typeof t === 'string' && t.length > 0 ? t : null;
  } catch {
    return null;
  }
}

/**
 * Sessions with work for the agent: any pending, due, wake-eligible inbound
 * row whose processing_ack is not terminal. A row stuck in 'processing' by a
 * dead runner counts as due — the respawned runner clears stale acks and
 * re-processes it (upstream crash-recovery semantics).
 */
export function sessionHasDueWork(dir) {
  let inbound;
  try {
    inbound = openInboundRo(dir);
  } catch {
    return false;
  }
  let pending;
  try {
    pending = inbound
      .prepare(
        `SELECT id FROM messages_in
         WHERE status = 'pending' AND trigger = 1
           AND (process_after IS NULL OR datetime(process_after) <= datetime('now'))`,
      )
      .all();
  } catch {
    return false;
  } finally {
    inbound.close();
  }
  if (pending.length === 0) return false;
  let outbound;
  try {
    outbound = openOutboundRo(dir);
  } catch {
    return true; // no outbound.db yet -> nothing acked -> due
  }
  try {
    const acked = new Set(
      outbound
        .prepare(`SELECT message_id FROM processing_ack WHERE status IN ('completed', 'failed', 'script-skip:error')`)
        .all()
        .map((r) => r.message_id),
    );
    return pending.some((r) => !acked.has(r.id));
  } catch {
    return true;
  } finally {
    outbound.close();
  }
}

// --- LLM routing state -----------------------------------------------------

/** Read the configure-llm state written by lib/configure-llm.mjs. */
export function readLlmConfig() {
  try {
    return JSON.parse(fs.readFileSync(LLM_CONFIG_PATH, 'utf8'));
  } catch {
    return null;
  }
}

/** Env block for agent-runner children derived from the routing document. */
export function llmEnv() {
  const cfg = readLlmConfig();
  if (!cfg || !cfg.proxy_url) return {};
  return {
    // NanoClaw's native Claude wiring: the Agent SDK honors these two.
    ANTHROPIC_BASE_URL: cfg.proxy_url,
    ANTHROPIC_AUTH_TOKEN: cfg.api_key || 'placeholder',
    // Bound the Claude Code CLI's API retry loop. Its default policy retries
    // even hard auth/connection failures for several minutes, which turns a
    // misconfigured virtual key into a multi-minute silent hang per turn.
    // Transient proxy blips still get a few attempts.
    CLAUDE_CODE_MAX_RETRIES: process.env.CLAWORC_NANOCLAW_MAX_RETRIES || '3',
  };
}

// --- chat event emission ---------------------------------------------------

export function emit(event) {
  process.stdout.write(JSON.stringify(event) + '\n');
}

export function jsonError(msg) {
  process.stdout.write(JSON.stringify({ error: msg }) + '\n');
}

export function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}
