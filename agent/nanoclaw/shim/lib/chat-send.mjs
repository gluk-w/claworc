// chat-send.mjs — bridge one Claworc chat turn onto NanoClaw's session-DB
// message contract (docs/shim.md on the Claworc side, docs/db-session.md on
// the NanoClaw side).
//
// Flow: insert the user message into the session's inbound.db (as the host
// process would), let the svc-agent supervisor wake an agent-runner child,
// then stream every new messages_out chat row as an assistant snapshot and
// finish when the runner writes a terminal processing_ack for our message id.
// The ack is a real end-of-turn marker written by the runner itself, so
// meta declares chat_end_detection "exact".
import fs from 'node:fs';

import {
  HOST_HEARTBEAT,
  ackStatus,
  chatPidFile,
  emit,
  ensureSession,
  insertUserMessage,
  maxOutboundSeq,
  mtimeAgeMs,
  outboundRowsAfter,
  outboundText,
  retireInboundMessages,
  runnerPidFile,
  pidAlive,
  readPidFile,
  sleep,
} from './shimlib.mjs';

const POLL_MS = 300;
// The svc-agent supervisor notices new work within ~0.5s and a runner boot
// (bun + Claude Agent SDK spawn) takes a few seconds; if no runner has even
// STARTED processing after this long, the service side is broken.
const RUNNER_GRACE_MS = 120_000;
// Absolute safety net so an orphaned chat-send cannot live forever. The
// control plane's own idle timeout (CLAWORC_WEBHOOK_IDLE_TIMEOUT / channel
// teardown) is the real supervisor of turn length.
const HARD_CAP_MS = 60 * 60 * 1000;

function usage(msg) {
  process.stderr.write(msg + '\n');
  process.exit(2);
}

let session = '';
let turn = '';
{
  const args = process.argv.slice(2);
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--session') session = args[++i] ?? '';
    else if (args[i] === '--turn') turn = args[++i] ?? '';
    else usage(`unknown argument: ${args[i]}`);
  }
}
if (!session) usage('--session is required');
if (!turn) turn = `t-${process.pid}-${Date.now().toString(36)}`;

const message = (await Bun.stdin.text()).toString();

let dir;
let messageId = null;
let lastText = '';
let endedOK = false;

function endEvent(stopReason, text) {
  if (endedOK) return;
  endedOK = true;
  emit({ v: 1, event: 'end', turn, stop_reason: stopReason, text: text ?? '' });
}

function cleanup() {
  try {
    fs.rmSync(chatPidFile(session), { force: true });
  } catch {
    /* ignore */
  }
}

function onAbortSignal() {
  // Retire our inbound row so a respawned runner doesn't re-process the
  // aborted turn (we own inbound.db — host-side status is authoritative for
  // the runner's pending query). History already streamed stays in the
  // session per contract. chat-abort separately SIGTERMs the runner.
  try {
    if (dir && messageId) retireInboundMessages(dir, [messageId]);
  } catch {
    /* best effort */
  }
  endEvent('aborted', lastText);
  cleanup();
  process.exit(0);
}
process.on('SIGTERM', onAbortSignal);
process.on('SIGINT', onAbortSignal);
process.on('SIGHUP', onAbortSignal);

try {
  dir = ensureSession(session);
  const baselineSeq = maxOutboundSeq(dir);

  try {
    fs.writeFileSync(chatPidFile(session), String(process.pid));
  } catch {
    /* chat-abort just won't find us */
  }

  emit({ v: 1, event: 'start', session, turn });
  messageId = insertUserMessage(dir, session, message);

  const startMs = Date.now();
  let lastSeq = baselineSeq;
  let sawRunner = false;
  let messageCount = 0;

  for (;;) {
    await sleep(POLL_MS);

    // Stream new outbound chat rows. Each messages_out row is one complete
    // assistant message — a cumulative snapshot keyed by its row id.
    for (const row of outboundRowsAfter(dir, lastSeq)) {
      lastSeq = row.seq;
      const text = outboundText(row);
      if (text === null) continue;
      messageCount++;
      lastText = text;
      emit({ v: 1, event: 'assistant', turn, message_id: row.id, text });
    }

    const ack = ackStatus(dir, messageId);
    if (ack === 'completed') {
      endEvent('complete', lastText);
      break;
    }
    if (ack === 'failed' || ack === 'script-skip:error') {
      emit({ v: 1, event: 'error', turn, code: 'agent_failed', text: `agent marked the message ${ack}`, fatal: true });
      endEvent('error', lastText);
      break;
    }

    const runnerPid = readPidFile(runnerPidFile(session));
    if (pidAlive(runnerPid)) sawRunner = true;

    // Service-side failure detection (all soft-fail as end/error, exit 0):
    if (mtimeAgeMs(HOST_HEARTBEAT) > 20_000) {
      emit({ v: 1, event: 'error', turn, code: 'agent_service_down', text: 'NanoClaw supervisor (svc-agent) is not running', fatal: true });
      retireInboundMessages(dir, [messageId]);
      endEvent('error', lastText);
      break;
    }
    if (!sawRunner && Date.now() - startMs > RUNNER_GRACE_MS && ack === null) {
      emit({ v: 1, event: 'error', turn, code: 'runner_not_started', text: 'no agent-runner started for this session in time', fatal: true });
      retireInboundMessages(dir, [messageId]);
      endEvent('error', lastText);
      break;
    }
    if (Date.now() - startMs > HARD_CAP_MS) {
      emit({ v: 1, event: 'error', turn, code: 'turn_timeout', text: 'turn exceeded the shim hard cap', fatal: true });
      retireInboundMessages(dir, [messageId]);
      endEvent('error', lastText);
      break;
    }
  }
} catch (err) {
  process.stderr.write(`chat-send: ${err?.stack || err}\n`);
  emit({ v: 1, event: 'error', turn, code: 'shim_error', text: String(err?.message || err), fatal: true });
  endEvent('error', lastText);
}

cleanup();
process.exit(0);
