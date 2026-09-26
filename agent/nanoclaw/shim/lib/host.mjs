// host.mjs — the shim-side replacement for the NanoClaw host process.
//
// Upstream NanoClaw's host (src/index.ts) does channels + routing + spawning
// one Docker container per session. In a Claworc instance the container IS
// the sandbox, and Claworc itself is the only channel, so this supervisor
// keeps just the one host responsibility that matters here: make sure an
// agent-runner child process is alive for every session that has pending
// work, and reap idle ones. All message IO stays exactly on NanoClaw's
// documented host<->runner contract (the per-session inbound.db/outbound.db
// SQLite pair, docs/db-session.md in the NanoClaw repo).
//
// Run by s6 (svc-agent) as the claworc user; logs to /var/log/claworc/agent.log
// via the service's redirect.
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

import {
  AGENT_DIR,
  HOST_HEARTBEAT,
  RUN_DIR,
  SESSIONS_DIR,
  llmEnv,
  runnerPidFile,
  sessionHasDueWork,
  sleep,
} from './shimlib.mjs';

const BUN = process.env.CLAWORC_SHIM_BUN || '/usr/local/bin/bun';
const RUNNER_ENTRY = '/app/src/index.ts';
const RUNNER_CWD = '/workspace/agent'; // symlink to AGENT_DIR (upstream path)
const TICK_MS = 500;
// Keep a warm runner (and its Claude Agent SDK subprocess) around between
// turns; upstream keeps containers warm and reaps them with a host sweep.
const IDLE_MS = parseInt(process.env.CLAWORC_NANOCLAW_RUNNER_IDLE_MS || '', 10) || 10 * 60 * 1000;

function log(msg) {
  process.stderr.write(`[shim-host] ${new Date().toISOString()} ${msg}\n`);
}

/** key -> { proc, lastActive } */
const runners = new Map();

function touchHeartbeat() {
  const now = new Date();
  try {
    fs.utimesSync(HOST_HEARTBEAT, now, now);
  } catch {
    try {
      fs.writeFileSync(HOST_HEARTBEAT, '');
    } catch {
      /* /run dir missing — init-setup creates it; retry next tick */
    }
  }
}

function spawnRunner(key, dir) {
  const env = {
    ...process.env,
    ...llmEnv(),
    HOME: process.env.HOME || '/home/claworc',
    NANOCLAW_SESSION_DIR: dir,
  };
  const proc = spawn(BUN, ['run', RUNNER_ENTRY], {
    cwd: RUNNER_CWD,
    env,
    stdio: ['ignore', 'inherit', 'inherit'],
  });
  const entry = { proc, lastActive: Date.now() };
  runners.set(key, entry);
  try {
    fs.writeFileSync(runnerPidFile(key), String(proc.pid));
  } catch {
    /* non-fatal: chat-abort just won't find the pid */
  }
  log(`runner spawned for session '${key}' (pid ${proc.pid})`);
  proc.on('exit', (code, signal) => {
    if (runners.get(key) === entry) runners.delete(key);
    try {
      fs.rmSync(runnerPidFile(key), { force: true });
    } catch {
      /* ignore */
    }
    log(`runner for session '${key}' exited (code=${code} signal=${signal})`);
  });
  proc.on('error', (err) => {
    if (runners.get(key) === entry) runners.delete(key);
    log(`runner spawn error for session '${key}': ${err.message}`);
  });
}

function stopRunner(key, reason) {
  const entry = runners.get(key);
  if (!entry) return;
  log(`stopping runner for session '${key}' (${reason})`);
  try {
    entry.proc.kill('SIGTERM');
  } catch {
    /* already gone */
  }
}

function tick() {
  touchHeartbeat();

  let keys = [];
  try {
    keys = fs
      .readdirSync(SESSIONS_DIR, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name);
  } catch {
    return; // sessions dir not created yet
  }

  for (const key of keys) {
    const dir = path.join(SESSIONS_DIR, key);
    if (!fs.existsSync(path.join(dir, 'inbound.db'))) continue;
    let due = false;
    try {
      due = sessionHasDueWork(dir);
    } catch (err) {
      log(`due-check failed for '${key}': ${err.message}`);
      continue;
    }
    const entry = runners.get(key);
    if (due) {
      if (entry) {
        entry.lastActive = Date.now();
      } else {
        spawnRunner(key, dir);
      }
    } else if (entry && Date.now() - entry.lastActive > IDLE_MS) {
      stopRunner(key, `idle ${Math.round(IDLE_MS / 1000)}s`);
    }
  }
}

async function main() {
  fs.mkdirSync(SESSIONS_DIR, { recursive: true });
  fs.mkdirSync(AGENT_DIR, { recursive: true });
  try {
    fs.mkdirSync(RUN_DIR, { recursive: true });
  } catch {
    /* created by init-setup as root; claworc may not own the parent */
  }

  let shuttingDown = false;
  const shutdown = (sig) => {
    if (shuttingDown) return;
    shuttingDown = true;
    log(`received ${sig}, stopping ${runners.size} runner(s)`);
    for (const key of runners.keys()) stopRunner(key, 'service shutdown');
    // Give runners a moment to finalize outbound.db writes, then exit; s6
    // escalates to SIGKILL on its own timeout if we hang.
    setTimeout(() => process.exit(0), 3000);
  };
  process.on('SIGTERM', () => shutdown('SIGTERM'));
  process.on('SIGINT', () => shutdown('SIGINT'));

  log(`supervising NanoClaw agent-runners (sessions: ${SESSIONS_DIR}, idle reap: ${IDLE_MS}ms)`);
  while (!shuttingDown) {
    try {
      tick();
    } catch (err) {
      log(`tick error: ${err.message}`);
    }
    await sleep(TICK_MS);
  }
}

main();
