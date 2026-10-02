#!/usr/bin/env node
// gateway-bridge.mjs — shared implementation behind the chat-send, chat-abort
// and session-reset shim verbs (see docs/shim.md for the contract).
//
// Speaks the OpenClaw local gateway WebSocket protocol on
// ws://127.0.0.1:18789/gateway and translates gateway event frames into the
// normalized Claworc chat JSONL on stdout.
//
// Connect handshake (a local backend client, not the browser Control UI):
//   1. token as ?token= query parameter, no Origin header,
//   2. read the connect.challenge frame (carries a nonce),
//   3. send a `connect` req (client gateway-client/backend, minProtocol 3,
//      maxProtocol 4, role operator, scopes ["operator.admin"], auth.token)
//      plus a device identity signing the nonce. The Ed25519 key persists in
//      DEVICE_FILE; the gateway pairs loopback devices silently.
//   4. wait for the res frame, skipping event frames; ok=false => auth failure.
//
// Usage: gateway-bridge.mjs <send|abort|reset|stream> --session <key> [--turn <id>]
// Node >= 22 only (relies on the built-in WebSocket global); no npm deps.
//
// `stream` backs the persistent chat-stream verb: one gateway connection for
// the whole session, commands on stdin (`send <turn> <base64>`, `abort`,
// `reset`), JSONL events on stdout — including turns the agent starts on its
// own (cron, heartbeats) for this session key.

import crypto, { randomUUID } from "node:crypto";
import fs, { readFileSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";
import net from "node:net";
import process from "node:process";

const GATEWAY_PORT = Number(process.env.OPENCLAW_GATEWAY_PORT || 18789);
const CONNECT_TIMEOUT_MS = 10_000;
// Persistent gateway device identity (Ed25519), on the claworc volume so the
// paired device survives restarts and image updates.
const DEVICE_FILE = process.env.CLAWORC_SHIM_DEVICE_FILE || "/home/claworc/.claworc/shim/gateway-device.json";
// SPKI DER of an Ed25519 public key = 12-byte header + 32-byte raw key.
const ED25519_SPKI_HEADER_LEN = 12;
// A local backend client (not the browser Control UI, which the gateway ties
// to a browser Origin and a matching UI build id), authenticated by the
// shared token plus its own paired device identity.
const CLIENT = { id: "gateway-client", displayName: "claworc-shim", version: "1.0.0", platform: "linux", mode: "backend" };
const ROLE = "operator";
const SCOPES = ["operator.admin"];
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
  // The gateway authenticates against its own config, so that token wins;
  // the env (which svc-agent/run copies into the config at boot) is only a
  // fallback for a missing or unreadable config.
  try {
    const cfg = JSON.parse(readFileSync("/home/claworc/.openclaw/openclaw.json", "utf8"));
    const t = cfg?.gateway?.auth?.token;
    if (typeof t === "string" && t !== "") return t;
  } catch {
    /* config missing/unreadable — fall back to the env */
  }
  return process.env.OPENCLAW_GATEWAY_TOKEN || process.env.CLAWORC_AGENT_TOKEN || "";
}

function parseArgs(argv) {
  const cmd = argv[0];
  if (!["send", "abort", "reset", "stream"].includes(cmd)) {
    die(EXIT_USAGE, `usage: gateway-bridge.mjs <send|abort|reset|stream> --session <key> [--turn <id>]`);
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

// chownToHomeOwner gives p to the owner of /home/claworc when running as
// root, so the identity stays readable by the agent user.
function chownToHomeOwner(p) {
  if (typeof process.getuid !== "function" || process.getuid() !== 0) return;
  try {
    const st = fs.statSync("/home/claworc");
    fs.chownSync(p, st.uid, st.gid);
  } catch {
    /* best effort */
  }
}

// loadOrCreateDevice returns {deviceId, publicKeyPem, privateKeyPem}. The id
// is sha256(raw public key) in hex, as the gateway derives it. Creation is
// race-safe: the file is written to a temp name and hard-linked into place,
// so concurrent verbs converge on whichever identity landed first.
function loadOrCreateDevice() {
  try {
    const d = JSON.parse(readFileSync(DEVICE_FILE, "utf8"));
    if (d?.deviceId && d?.publicKeyPem && d?.privateKeyPem) return d;
  } catch {
    /* missing or corrupt — create below */
  }
  const dir = path.dirname(DEVICE_FILE);
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  chownToHomeOwner(dir);
  const { publicKey, privateKey } = crypto.generateKeyPairSync("ed25519");
  const raw = publicKey.export({ type: "spki", format: "der" }).subarray(ED25519_SPKI_HEADER_LEN);
  const device = {
    deviceId: crypto.createHash("sha256").update(raw).digest("hex"),
    publicKeyPem: publicKey.export({ type: "spki", format: "pem" }),
    privateKeyPem: privateKey.export({ type: "pkcs8", format: "pem" }),
  };
  const tmp = `${DEVICE_FILE}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, JSON.stringify(device), { mode: 0o600 });
  chownToHomeOwner(tmp);
  try {
    fs.linkSync(tmp, DEVICE_FILE);
  } catch (err) {
    if (err.code !== "EEXIST") throw err;
    fs.unlinkSync(tmp);
    return JSON.parse(readFileSync(DEVICE_FILE, "utf8"));
  }
  fs.unlinkSync(tmp);
  return device;
}

// deviceProof signs the gateway's v3 device-auth payload:
// v3|deviceId|clientId|clientMode|role|scopes|signedAtMs|token|nonce|platform|deviceFamily
function deviceProof(device, nonce, token) {
  const signedAt = Date.now();
  const payload = [
    "v3", device.deviceId, CLIENT.id, CLIENT.mode, ROLE, SCOPES.join(","),
    String(signedAt), token || "", nonce, CLIENT.platform.trim().toLowerCase(), "",
  ].join("|");
  const signature = crypto
    .sign(null, Buffer.from(payload, "utf8"), crypto.createPrivateKey(device.privateKeyPem))
    .toString("base64url");
  const publicKey = crypto
    .createPublicKey(device.publicKeyPem)
    .export({ type: "spki", format: "der" })
    .subarray(ED25519_SPKI_HEADER_LEN)
    .toString("base64url");
  return { id: device.deviceId, publicKey, signature, signedAt, nonce };
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

  // No Origin header: the bridge is a local non-browser client.
  const ws = new WebSocket(url);

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

  // Phase 1: the gateway sends a connect.challenge frame first; its nonce is
  // signed into the device proof.
  const challenge = await gw.next(CONNECT_TIMEOUT_MS);
  if (challenge === null) throw new Error("gateway closed before the connect challenge");
  const nonce = String(challenge?.payload?.nonce ?? "").trim();

  // Phase 2: connect request — gateway_dialer.go plus the device proof.
  gw.send({
    type: "req",
    id: `connect-${Date.now()}`,
    method: "connect",
    params: {
      minProtocol: 3,
      maxProtocol: 4,
      // client.id must be one of the gateway's known client ids
      // ("claworc-shim" is rejected with "invalid connect params").
      client: CLIENT,
      role: ROLE,
      scopes: SCOPES,
      auth: { token },
      ...(nonce ? { device: deviceProof(loadOrCreateDevice(), nonce, token) } : {}),
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
// stream
// ---------------------------------------------------------------------------

// The gateway may report a canonicalized session key (e.g. "agent:main:<key>").
function sessionMatches(eventKey, session) {
  if (typeof eventKey !== "string" || eventKey === "") return false;
  const a = eventKey.toLowerCase();
  const b = session.toLowerCase();
  return a === b || a.endsWith(`:${b}`);
}

// One turn's output state: start/assistant/tool/end lines with snapshot
// throttling. Used for both our own turns and unsolicited ones.
class Turn {
  constructor(out, session, turn, runId) {
    this.out = out;
    this.session = session;
    this.turn = turn;
    this.runId = runId;
    this.started = false;
    this.ended = false;
    this.lastText = "";
    this.pending = null;
    this.lastEmit = 0;
    this.flushTimer = null;
    this.idleTimer = null;
  }

  ensureStart() {
    if (this.started) return;
    this.started = true;
    this.out({ v: 1, event: "start", session: this.session, turn: this.turn });
  }

  flush() {
    if (this.flushTimer) {
      clearTimeout(this.flushTimer);
      this.flushTimer = null;
    }
    if (!this.pending) return;
    this.ensureStart();
    this.out({ v: 1, event: "assistant", turn: this.turn, message_id: this.pending.messageId, text: this.pending.text });
    this.lastEmit = Date.now();
    this.pending = null;
  }

  snapshot(messageId, text) {
    this.lastText = text;
    if (this.pending && this.pending.messageId !== messageId) this.flush();
    this.pending = { messageId, text };
    const wait = SNAPSHOT_THROTTLE_MS - (Date.now() - this.lastEmit);
    if (wait <= 0) this.flush();
    else if (!this.flushTimer) this.flushTimer = setTimeout(() => this.flush(), wait);
  }

  tool(data) {
    this.flush();
    this.ensureStart();
    const ev = { v: 1, event: "tool", turn: this.turn, name: "tool", detail: data };
    if (typeof data.name === "string" && data.name !== "") ev.name = data.name;
    else if (typeof data.tool === "string" && data.tool !== "") ev.name = data.tool;
    if (typeof data.phase === "string" && data.phase !== "") ev.phase = data.phase;
    this.out(ev);
  }

  error(code, text) {
    this.flush();
    this.ensureStart();
    this.out({ v: 1, event: "error", turn: this.turn, code: String(code), text: String(text), fatal: true });
  }

  finish(stopReason) {
    if (this.ended) return;
    this.ended = true;
    if (this.idleTimer) clearTimeout(this.idleTimer);
    this.flush();
    this.ensureStart();
    this.out({ v: 1, event: "end", turn: this.turn, stop_reason: stopReason, text: this.lastText });
  }
}

async function cmdStream(session) {
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
  out({ v: 1, event: "ready" });

  const queue = []; // {kind: "send", turn, message} | {kind: "reset"}
  const runs = new Map(); // runId -> Turn (own and unsolicited, until end)
  const dropped = new Set(); // runIds of aborted own turns: ignore late frames
  const resets = new Map(); // reqId -> resolve(res frame)
  let active = null; // our own in-flight Turn
  let busy = false; // a queued command (send or reset) is in progress
  let seq = 0;

  const send = (frame) => {
    try {
      gw.send(frame);
    } catch {
      /* socket closing — handled by the close path */
    }
  };

  const armIdle = (t) => {
    if (t.idleTimer) clearTimeout(t.idleTimer);
    t.idleTimer = setTimeout(() => {
      t.error("idle_timeout", `no gateway events for ${IDLE_TIMEOUT_MS}ms`);
      endTurn(t, "error");
    }, IDLE_TIMEOUT_MS);
  };

  const endTurn = (t, stopReason) => {
    t.finish(stopReason);
    runs.delete(t.runId);
    if (t === active) {
      active = null;
      busy = false;
      pump();
    }
  };

  const pump = () => {
    if (busy || queue.length === 0) return;
    const item = queue.shift();
    busy = true;
    if (item.kind === "reset") {
      const reqId = `reset-${++seq}`;
      const timer = setTimeout(() => finishReset({ ok: false, error: { message: "timed out" } }), CONNECT_TIMEOUT_MS);
      const finishReset = (res) => {
        clearTimeout(timer);
        if (!resets.delete(reqId)) return;
        if (res && res.ok === false) {
          const msg = String(res?.error?.message || "rejected");
          if (!/not found|unknown|no such|missing/i.test(msg)) {
            out({ v: 1, event: "error", code: "reset_failed", text: `sessions.reset failed: ${msg}`, fatal: false });
          }
        }
        busy = false;
        pump();
      };
      resets.set(reqId, finishReset);
      send({ type: "req", id: reqId, method: "sessions.reset", params: { key: session } });
      return;
    }
    const runId = randomUUID();
    const t = new Turn(out, session, item.turn, runId);
    t.reqId = `chat-${++seq}`;
    active = t;
    runs.set(runId, t);
    t.ensureStart();
    armIdle(t);
    send({
      type: "req",
      id: t.reqId,
      method: "chat.send",
      params: { sessionKey: session, message: item.message, idempotencyKey: runId },
    });
  };

  const abortActive = () => {
    if (!active) return;
    const t = active;
    send({ type: "req", id: `abort-${++seq}`, method: "chat.abort", params: { sessionKey: session } });
    dropped.add(t.runId);
    endTurn(t, "aborted");
  };

  const shutdown = (code) => {
    gw.close();
    process.exit(code);
  };

  // Commands on stdin.
  const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
  rl.on("line", (line) => {
    const [cmd = "", turn = "", b64 = ""] = line.trim().split(/\s+/);
    switch (cmd) {
      case "send":
        if (!turn) {
          process.stderr.write("chat-stream: send without a turn id\n");
          return;
        }
        queue.push({ kind: "send", turn, message: Buffer.from(b64, "base64").toString("utf8") });
        pump();
        break;
      case "reset":
        queue.push({ kind: "reset" });
        pump();
        break;
      case "abort":
        abortActive();
        break;
      case "":
        break;
      default:
        process.stderr.write(`chat-stream: unknown command: ${cmd}\n`);
    }
  });
  rl.on("close", () => {
    // stdin EOF: drop queued commands, abort the in-flight turn, exit 0.
    queue.length = 0;
    abortActive();
    shutdown(EXIT_OK);
  });
  const onSignal = () => {
    queue.length = 0;
    abortActive();
    shutdown(EXIT_OK);
  };
  process.on("SIGTERM", onSignal);
  process.on("SIGINT", onSignal);
  process.on("SIGHUP", onSignal);

  // Gateway frames.
  for (;;) {
    const frame = await gw.next(24 * 3600 * 1000).catch(() => undefined);
    if (frame === undefined) continue; // no traffic for a day: keep waiting
    if (frame === null) {
      for (const t of runs.values()) {
        t.error("gateway_closed", "gateway connection closed mid-turn");
        t.finish("error");
      }
      process.stderr.write("gateway connection closed\n");
      process.exit(EXIT_INTERNAL);
    }

    if (frame.type === "res") {
      const r = resets.get(frame.id);
      if (r) {
        r(frame);
        continue;
      }
      if (active && frame.id === active.reqId && frame.ok === false) {
        const t = active;
        t.error(frame?.error?.code || "gateway_error", frame?.error?.message || "chat.send rejected");
        endTurn(t, "error");
      }
      continue;
    }
    if (frame.type !== "event") continue;
    const payload = frame.payload;
    if (!payload || typeof payload !== "object") continue;
    const runId = String(payload.runId ?? "");
    if (!runId || dropped.has(runId)) continue;
    const data = payload.data && typeof payload.data === "object" ? payload.data : {};

    let t = runs.get(runId);
    if (!t) {
      // Not one of ours: only turns the agent runs for this session key, and
      // only from their start (a mid-run frame has no turn to attach to).
      if (!sessionMatches(payload.sessionKey, session)) continue;
      if (payload.stream !== "lifecycle" || data.phase !== "start") continue;
      t = new Turn(out, session, `u-${runId.slice(0, 12)}`, runId);
      runs.set(runId, t);
    }
    if (t === active) armIdle(t);

    switch (payload.stream) {
      case "assistant":
        if (typeof data.text === "string" && data.text !== "") {
          t.snapshot(String(runId), data.text);
        }
        break;
      case "tool":
        t.tool(data);
        break;
      case "lifecycle":
        if (data.phase === "start") t.ensureStart();
        else if (data.phase === "end") endTurn(t, "complete");
        else if (data.phase === "error") {
          t.error("agent_failed", data.error || data.message || "agent run failed");
          endTurn(t, "error");
        }
        break;
      default:
        break;
    }
  }
}

// ---------------------------------------------------------------------------

const { cmd, session, turn } = parseArgs(process.argv.slice(2));

const run = {
  send: () => cmdSend(session, turn),
  abort: () => cmdAbort(session),
  reset: () => cmdReset(session),
  stream: () => cmdStream(session),
}[cmd];

run().catch((err) => {
  die(err.timeout ? EXIT_TIMEOUT : EXIT_INTERNAL, `gateway-bridge ${cmd}: ${err.message}`);
});
