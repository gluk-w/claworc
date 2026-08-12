// chat-abort.mjs — abort the in-flight turn for a session (docs/shim.md).
//
// Best-effort, exit 0 even when nothing was running:
//   1. retire the session's un-acked pending inbound rows so a respawned
//      runner won't re-process the aborted turn,
//   2. SIGTERM the session's agent-runner (kills the in-flight SDK query;
//      the supervisor lazily respawns on the next real message),
//   3. SIGTERM the running chat-send, which emits end/aborted and exits 0.
import fs from 'node:fs';
import path from 'node:path';

import {
  chatPidFile,
  openInboundRo,
  pidAlive,
  readPidFile,
  retireInboundMessages,
  runnerPidFile,
  sessionDir,
} from './shimlib.mjs';

let session = '';
{
  const args = process.argv.slice(2);
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--session') session = args[++i] ?? '';
    else {
      process.stderr.write(`unknown argument: ${args[i]}\n`);
      process.exit(2);
    }
  }
}
if (!session) {
  process.stderr.write('--session is required\n');
  process.exit(2);
}

const dir = sessionDir(session);

// 1. retire pending rows (idempotent; session may not exist at all).
try {
  if (fs.existsSync(path.join(dir, 'inbound.db'))) {
    const db = openInboundRo(dir);
    let ids = [];
    try {
      ids = db.prepare(`SELECT id FROM messages_in WHERE status = 'pending'`).all().map((r) => r.id);
    } finally {
      db.close();
    }
    retireInboundMessages(dir, ids);
  }
} catch (err) {
  process.stderr.write(`chat-abort: retire failed: ${err.message}\n`);
}

// 2. kill the runner.
try {
  const pid = readPidFile(runnerPidFile(session));
  if (pidAlive(pid)) process.kill(pid, 'SIGTERM');
} catch {
  /* already gone */
}

// 3. signal chat-send (it emits end/aborted itself).
try {
  const pid = readPidFile(chatPidFile(session));
  if (pidAlive(pid)) process.kill(pid, 'SIGTERM');
} catch {
  /* already gone */
}

process.exit(0);
