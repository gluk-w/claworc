// session-reset.mjs — clear conversation history for a Claworc session key
// (docs/shim.md). Idempotent.
//
// A NanoClaw session's entire conversational state lives in its session
// directory: the inbound/outbound DB pair, including the SDK continuation id
// in outbound.db's session_state. Killing the runner and deleting the
// directory gives the next chat-send a completely fresh session (equivalent
// to upstream's /clear plus a new DB pair). The shared agent workspace
// (files + long-term memory under /home/claworc/workspace) is intentionally
// NOT touched — that is agent-level state shared by every session, exactly as
// in upstream NanoClaw.
import fs from 'node:fs';

import { pidAlive, readPidFile, runnerPidFile, sessionDir, sleep } from './shimlib.mjs';

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

const pid = readPidFile(runnerPidFile(session));
if (pidAlive(pid)) {
  try {
    process.kill(pid, 'SIGTERM');
  } catch {
    /* gone */
  }
  // Give it a moment to release its DB handles before we delete the files.
  for (let i = 0; i < 20 && pidAlive(pid); i++) await sleep(100);
  if (pidAlive(pid)) {
    try {
      process.kill(pid, 'SIGKILL');
    } catch {
      /* gone */
    }
  }
}

fs.rmSync(sessionDir(session), { recursive: true, force: true });
fs.rmSync(runnerPidFile(session), { force: true });
process.exit(0);
