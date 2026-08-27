#!/usr/bin/env node
// gateway-bridge.mjs — shared implementation behind the chat-send, chat-abort
// and session-reset shim verbs (see docs/shim.md for the contract).
//
// Speaks the OpenClaw local gateway WebSocket protocol on
// ws://127.0.0.1:18789/gateway and translates gateway event frames into the
// normalized Claworc chat JSONL on stdout.
//
// The connect handshake replicates the control plane's Go client
// (control-plane/internal/sshproxy/gateway_dialer.go) frame-for-frame:
//   1. token as ?token= query parameter + Origin header,
//   2. read one challenge frame,
//   3. send a `connect` req (minProtocol 3, maxProtocol 4, role operator,
//      scopes ["operator.admin"], auth.token),
//   4. wait for the res frame, skipping event frames; ok=false => auth failure.
//
// Usage: gateway-bridge.mjs <send|abort|reset> --session <key> [--turn <id>]
// Node >= 22 only (relies on the built-in WebSocket global); no npm deps.

import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import net from "node:net";
import process from "node:process";

const GATEWAY_PORT = Number(process.env.OPENCLAW_GATEWAY_PORT || 18789);
const GATEWAY_ORIGIN = `http://127.0.0.1:${GATEWAY_PORT}`;
const CONNECT_TIMEOUT_MS = 10_000;
// Idle gap tolerated between gateway frames during a chat turn. Re-armed on
// every frame, so an actively streaming agent is never cut off.
const IDLE_TIMEOUT_MS = Number(process.env.CLAWORC_SHIM_CHAT_IDLE_MS || 300_000);
// Minimum interval between assistant snapshot lines (contract: >= 150 ms).
const SNAPSHOT_THROTTLE_MS = 150;

// Contract exit codes (docs/shim.md).
const EXIT_OK = 0;
const EXIT_INTERNAL = 1;
const EXIT_USAGE = 2;
const EXIT_NOT_READY = 4;
const EXIT_TIMEOUT = 5;

function die(code, msg) {
  if (msg) process.stderr.write(`${msg}\n`);
  process.exit(code);
}

function resolveToken() {
  if (process.env.OPENCLAW_GATEWAY_TOKEN) return process.env.OPENCLAW_GATEWAY_TOKEN;
  if (process.env.CLAWORC_AGENT_TOKEN) return process.env.CLAWORC_AGENT_TOKEN;
  // Fallback: read the token straight out of the agent config so the verbs
  // work even in exec contexts that did not inherit the container env.
  try {
    const cfg = JSON.parse(readFileSync("/home/claworc/.openclaw/openclaw.json", "utf8"));
    const t = cfg?.gateway?.auth?.token;
    if (typeof t === "string" && t !== "") return t;
  } catch {
    /* config missing/unreadable — proceed tokenless */
  }
  return "";
}

function parseArgs(argv) {
  const cmd = argv[0];
  if (!["send", "abort", "reset"].includes(cmd)) {
    die(EXIT_USAGE, `usage: gateway-bridge.mjs <send|abort|reset> --session <key> [--turn <id>]`);
  }
  let session = "";
  let turn = "";
  for (let i = 1; i < argv.length; i++) {
    switch (argv[i]) {
      case "--session":
        session = argv[++i] ?? "";
        break;
      case "--turn":
        turn = argv[++i] ?? "";
        break;
      default:
        die(EXIT_USAGE, `unknown argument: ${argv[i]}`);
    }
  }
  if (!session) die(EXIT_USAGE, "--session is required");
  if (!turn) turn = `t-${randomUUID().slice(0, 8)}`;
  return { cmd, session, turn };
}

// Quick TCP probe so "agent still booting" (exit 4) is distinguishable from
// genuine dial/handshake failures (exit 1).
function probePort(port, timeoutMs = 3000) {
  return new Promise((resolve) => {
    const sock = net.connect({ host: "127.0.0.1", port });
    const done = (up) => {
      sock.removeAllListeners();
      sock.destroy();
      resolve(up);
    };
    sock.setTimeout(timeoutMs, () => done(false));
    sock.once("connect", () => done(true));
    sock.once("error", () => done(false));
  });
}

class Gateway {
  constructor(ws) {
    this.ws = ws;
    this.queue = [];
    this.waiter = null; // {resolve} of a pending next()
    this.closed = false;
    ws.addEventListener("message", (ev) => {
      let frame;
      try {
        frame = JSON.parse(typeof ev.data === "string" ? ev.data : String(ev.data));
      } catch {
        return; // ignore non-JSON frames
      }
      this.push(frame);
    });
    ws.addEventListener("close", () => {
      this.closed = true;
      this.push(null);
    });
    ws.addEventListener("error", () => {
      this.closed = true;
      this.push(null);
    });
  }

  push(item) {
    if (this.waiter) {
      const w = this.waiter;
      this.waiter = null;
      w.resolve(item);
    } else {
      this.queue.push(item);
    }
  }

  // Resolves with the next parsed frame, null when the socket closed, or
  // rejects with a TimeoutError after timeoutMs of silence.
  next(timeoutMs) {
    if (this.queue.length > 0) return Promise.resolve(this.queue.shift());
    if (this.closed) return Promise.resolve(null);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        if (this.waiter && this.waiter.resolve === wrapped) this.waiter = null;
        const err = new Error(`no gateway frame for ${timeoutMs}ms`);
        err.timeout = true;
        reject(err);
      }, Math.max(1, timeoutMs));
      const wrapped = (item) => {
        clearTimeout(timer);
        resolve(item);
      };
      this.waiter = { resolve: wrapped };
    });
  }

  send(frame) {
    this.ws.send(JSON.stringify(frame));
  }

  close() {
    try {
      this.ws.close();
    } catch {
      /* already closed */
    }
  }
}

async function dialGateway(token) {
  if (typeof WebSocket === "undefined") {
    throw new Error("global WebSocket unavailable — node >= 22 required");
  }
  let url = `ws://127.0.0.1:${GATEWAY_PORT}/gateway`;
  if (token) url += `?token=${encodeURIComponent(token)}`;

  let ws;
  try {
    // Node's undici WebSocket accepts a non-standard `headers` option; the
    // gateway expects a loopback Origin (mirrors gateway_dialer.go).
    ws = new WebSocket(url, { headers: { Origin: GATEWAY_ORIGIN } });
  } catch {
    ws = new WebSocket(url);
  }

  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      const err = new Error("timed out opening gateway websocket");
      err.timeout = true;
      reject(err);
    }, CONNECT_TIMEOUT_MS);
    ws.addEventListener("open", () => {
      clearTimeout(timer);
      resolve();
    }, { once: true });
    ws.addEventListener("error", () => {
      clearTimeout(timer);
      reject(new Error("gateway websocket dial failed"));
    }, { once: true });
  });

  const gw = new Gateway(ws);

  // Phase 1: the gateway sends a connect.challenge frame first.
  await gw.next(CONNECT_TIMEOUT_MS);

  // Phase 2: connect request — same frame shape as gateway_dialer.go.
  gw.send({
    type: "req",
    id: `connect-${Date.now()}`,
    method: "connect",
    params: {
      minProtocol: 3,
      maxProtocol: 4,
      // The gateway validates client.id against an allowlist of known
      // clients, so this must stay byte-identical to the control plane's
      // dialer ("claworc-shim" gets rejected with "invalid connect params").
      client: {
        id: "openclaw-control-ui",
        version: "1.0.0",
        platform: "linux",
        mode: "webchat",
      },
      role: "operator",
      scopes: ["operator.admin"],
      auth: { token },
    },
  });

  // Phase 3: wait for the hello-ok response, skipping event frames.
  const deadline = Date.now() + CONNECT_TIMEOUT_MS;
  for (;;) {
    const frame = await gw.next(Math.max(1, deadline - Date.now()));
    if (frame === null) throw new Error("gateway closed during handshake");
    if (frame.type === "event") continue;
    if (frame.type === "res") {
      if (frame.ok !== true) {
        const msg = frame?.error?.message || "gateway auth failed";
        const err = new Error(msg);
        err.handshake = true;
        throw err;
      }
      return gw;
    }
  }
}

// ---------------------------------------------------------------------------
// send
// ---------------------------------------------------------------------------

async function cmdSend(session, turn) {
  const message = readFileSync(0, "utf8"); // stdin until EOF

  if (!(await probePort(GATEWAY_PORT))) {
    die(EXIT_NOT_READY, `gateway port ${GATEWAY_PORT} is not accepting connections (agent still booting?)`);
  }

  let gw;
  try {
    gw = await dialGateway(resolveToken());
  } catch (err) {
    die(err.timeout ? EXIT_TIMEOUT : EXIT_INTERNAL, `gateway handshake failed: ${err.message}`);
  }

  const out = (obj) => process.stdout.write(`${JSON.stringify(obj)}\n`);

  let started = false;
  let ended = false;
  let lastText = ""; // last assistant snapshot — becomes end.text

  const ensureStart = () => {
    if (!started) {
      started = true;
      out({ v: 1, event: "start", session, turn });
    }
  };

  // Assistant snapshot throttling (>=150ms apart, flushed on message
  // boundaries, tool events, and end).
  let pending = null; // {messageId, text}
  let lastEmit = 0;
  let flushTimer = null;
  const flushPending = () => {
    if (flushTimer) {
      clearTimeout(flushTimer);
      flushTimer = null;
    }
    if (!pending) return;
    ensureStart();
    out({ v: 1, event: "assistant", turn, message_id: pending.messageId, text: pending.text });
    lastEmit = Date.now();
    pending = null;
  };
  const snapshot = (messageId, text) => {
    lastText = text;
    if (pending && pending.messageId !== messageId) flushPending();
    pending = { messageId, text };
    const wait = SNAPSHOT_THROTTLE_MS - (Date.now() - lastEmit);
    if (wait <= 0) flushPending();
    else if (!flushTimer) flushTimer = setTimeout(flushPending, wait);
  };

  const finish = (stopReason) => {
    if (ended) return;
    ended = true;
    flushPending();
    ensureStart();
    out({ v: 1, event: "end", turn, stop_reason: stopReason, text: lastText });
    gw.close();
    process.exit(EXIT_OK);
  };

  // Abort semantics: SIGTERM (or SSH channel teardown) aborts the in-flight
  // turn, emits end/aborted, exits 0.
  const onAbortSignal = () => {
    try {
      gw.send({
        type: "req",
        id: `abort-${Date.now()}`,
        method: "chat.abort",
        params: { sessionKey: session },
      });
    } catch {
      /* best-effort */
    }
    finish("aborted");
  };
  process.on("SIGTERM", onAbortSignal);
  process.on("SIGINT", onAbortSignal);
  process.on("SIGHUP", onAbortSignal);

  const reqId = `chat-${Date.now()}`;
  gw.send({
    type: "req",
    id: reqId,
    method: "chat.send",
    params: {
      sessionKey: session,
      message,
      idempotencyKey: randomUUID(),
    },
  });

  for (;;) {
    let frame;
    try {
      frame = await gw.next(IDLE_TIMEOUT_MS);
    } catch (err) {
      if (err.timeout) {
        out({ v: 1, event: "error", turn, code: "idle_timeout", text: `no gateway events for ${IDLE_TIMEOUT_MS}ms`, fatal: true });
        finish("error");
      }
      throw err;
    }
    if (frame === null) {
      // Socket closed without a lifecycle end.
      out({ v: 1, event: "error", turn, code: "gateway_closed", text: "gateway connection closed mid-turn", fatal: true });
      finish("error");
    }

    if (frame.type === "res") {
      if (frame.id === reqId && frame.ok === false) {
        const msg = frame?.error?.message || "chat.send rejected";
        const code = frame?.error?.code || "gateway_error";
        out({ v: 1, event: "error", turn, code: String(code), text: String(msg), fatal: true });
        finish("error");
      }
      continue; // ok-acks carry no chat content
    }
    if (frame.type !== "event") continue;
    const payload = frame.payload;
    if (!payload || typeof payload !== "object") continue;
    const data = payload.data && typeof payload.data === "object" ? payload.data : {};

    switch (payload.stream) {
      case "assistant": {
        // OpenClaw assistant events carry the CUMULATIVE snapshot in
        // data.text — exactly what the contract's assistant.text wants.
        if (typeof data.text === "string" && data.text !== "") {
          const messageId = String(payload.runId ?? data.runId ?? "m1");
          snapshot(messageId, data.text);
        }
        break;
      }
      case "tool": {
        flushPending(); // keep assistant/tool ordering
        ensureStart();
        const ev = { v: 1, event: "tool", turn, name: "tool", detail: data };
        if (typeof data.name === "string" && data.name !== "") ev.name = data.name;
        else if (typeof data.tool === "string" && data.tool !== "") ev.name = data.tool;
        if (typeof data.phase === "string" && data.phase !== "") ev.phase = data.phase;
        out(ev);
        break;
      }
      case "lifecycle": {
        const phase = typeof data.phase === "string" ? data.phase : "";
        if (phase === "start") ensureStart();
        else if (phase === "end") finish("complete");
        break;
      }
      default:
        break; // unknown streams are ignored (forward compatibility)
    }
  }
}

// ---------------------------------------------------------------------------
// abort / reset
// ---------------------------------------------------------------------------

async function sendSimpleRequest(method, params, reqPrefix) {
  const gw = await dialGateway(resolveToken());
  const reqId = `${reqPrefix}-${Date.now()}`;
  gw.send({ type: "req", id: reqId, method, params });
  const deadline = Date.now() + CONNECT_TIMEOUT_MS;
  for (;;) {
    const frame = await gw.next(Math.max(1, deadline - Date.now()));
    if (frame === null) return null;
    if (frame.type === "res" && frame.id === reqId) {
      gw.close();
      return frame;
    }
  }
}

async function cmdAbort(session) {
  // "Exit 0 also when nothing was running" — a gateway that is not even
  // listening trivially has no turn in flight.
  if (!(await probePort(GATEWAY_PORT))) {
    process.stderr.write("gateway not listening; nothing to abort\n");
    process.exit(EXIT_OK);
  }
  try {
    const res = await sendSimpleRequest("chat.abort", { sessionKey: session }, "abort");
    if (res && res.ok === false) {
      process.stderr.write(`chat.abort: ${res?.error?.message || "rejected"} (treated as no-op)\n`);
    }
  } catch (err) {
    process.stderr.write(`chat.abort best-effort failed: ${err.message}\n`);
  }
  process.exit(EXIT_OK);
}

async function cmdReset(session) {
  if (!(await probePort(GATEWAY_PORT))) {
    die(EXIT_NOT_READY, `gateway port ${GATEWAY_PORT} is not accepting connections (agent still booting?)`);
  }
  let res;
  try {
    // Frame shape mirrors the control plane chat proxy (handlers/chat.go):
    // method sessions.reset, params {key: <session key>}.
    res = await sendSimpleRequest("sessions.reset", { key: session }, "reset");
  } catch (err) {
    die(err.timeout ? EXIT_TIMEOUT : EXIT_INTERNAL, `sessions.reset failed: ${err.message}`);
  }
  if (res && res.ok === false) {
    const msg = String(res?.error?.message || "rejected");
    // Resetting a session that does not exist yet is a success (idempotency).
    if (/not found|unknown|no such|missing/i.test(msg)) process.exit(EXIT_OK);
    die(EXIT_INTERNAL, `sessions.reset rejected: ${msg}`);
  }
  process.exit(EXIT_OK);
}

// ---------------------------------------------------------------------------

const { cmd, session, turn } = parseArgs(process.argv.slice(2));

const run = { send: () => cmdSend(session, turn), abort: () => cmdAbort(session), reset: () => cmdReset(session) }[cmd];

run().catch((err) => {
  die(err.timeout ? EXIT_TIMEOUT : EXIT_INTERNAL, `gateway-bridge ${cmd}: ${err.message}`);
});
