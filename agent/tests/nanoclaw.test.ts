/**
 * Integration tests for the claworc/nanoclaw agent image (agent/nanoclaw/).
 *
 * NanoClaw (nanocoai/nanoclaw v2) is a host process + one Bun agent-runner
 * (Claude Agent SDK) per session, talking only through a per-session SQLite
 * pair. The image replaces the upstream host with the shim's supervisor
 * (shim/lib/host.mjs under s6 `svc-agent`) and runs runners as plain child
 * processes. What we verify here:
 *   - the pinned toolchain (bun, node 22, Claude Code CLI) and the /app,
 *     /workspace/agent layout upstream hardcodes, plus the build-time patch,
 *   - the CLAWORC_INITIAL_LLM_CONFIG boot path: llm.json + container.json,
 *   - the svc-agent supervisor (runs as claworc, heartbeat, restart verb),
 *   - every shim verb per docs/shim.md, including validation and idempotency,
 *   - a real chat turn through the session-DB contract with no LLM reachable.
 *
 * Ordering matters: boot-state assertions first, mutating verbs after,
 * shim-selftest last.
 */
import { describe, it, expect, beforeAll, beforeEach, afterEach, afterAll } from "vitest";
import {
  exec,
  execAsUser,
  getContainers,
  dumpDiagnostics,
  parseJsonl,
  shim,
  waitFor,
} from "./helpers";
import { llmConfigFor, nanoclawLlmJson, nanoclawMeta, DEFAULT_MODEL } from "./fixtures";
import {
  expectValidationError,
  owner,
  mode,
  readFile,
  runShimSelftest,
  shimContractTests,
} from "./shim-contract";

const containers = getContainers();
const container = containers.nanoclaw?.name;

const WORKSPACE = "/home/claworc/workspace";
const CONFIG = `${WORKSPACE}/container.json`;
const STATE_DIR = "/home/claworc/.claworc/shim/nanoclaw";
const LLM_JSON = `${STATE_DIR}/llm.json`;
const SESSIONS_DIR = `${STATE_DIR}/sessions`;
const RUN_DIR = "/run/claworc/shim";
const SVC = "/run/service/svc-agent";
const AGENT_LOG = "/var/log/claworc/agent.log";
const BOOT_DOC = llmConfigFor("nanoclaw");
const SESSION = "vitest";

/** `s6-svstat -o <field>` for svc-agent, e.g. field "up" or "pid". */
function svstat(field: string): string {
  const r = exec(container!, ["/command/s6-svstat", "-o", field, SVC]);
  expect(r.exitCode, r.stderr).toBe(0);
  return r.stdout.trim();
}

function readJson(path: string): any {
  return JSON.parse(readFile(container!, path));
}

function applyLlm(doc: unknown): void {
  const r = shim(container!, "configure-llm", [], JSON.stringify(doc));
  expect(r.exitCode, r.stdout + r.stderr).toBe(0);
}

function assertChatContract(events: any[], session: string, turn: string): void {
  expect(events.length).toBeGreaterThanOrEqual(2);
  for (const ev of events) {
    expect(ev.v).toBe(1);
    expect(ev.turn).toBe(turn);
  }
  expect(events[0].event).toBe("start");
  expect(events[0].session).toBe(session);
  const ends = events.filter((e) => e.event === "end");
  expect(ends).toHaveLength(1);
  const last = events[events.length - 1];
  expect(last.event).toBe("end");
  expect(["complete", "aborted", "error"]).toContain(last.stop_reason);
  expect(typeof last.text).toBe("string");
}

describe.skipIf(!container)("nanoclaw image", { timeout: 120_000 }, () => {
  beforeAll(async () => {
    // health exits 4 until the svc-agent supervisor is up and its heartbeat
    // is fresh (<= 15 s). init-setup applies CLAWORC_INITIAL_LLM_CONFIG and
    // init-agent-seed seeds the workspace before svc-agent starts, so a
    // healthy shim also implies the boot state below is in place.
    const healthy = await waitFor(() => shim(container!, "health").exitCode === 0, 300_000, 3_000);
    if (!healthy) {
      dumpDiagnostics(container!);
      throw new Error("nanoclaw: shim health never returned 0");
    }
  }, 960_000);

  afterAll(() => {
    if (!container) return;
    // A timed-out `docker exec` does not kill the in-container chat-send.
    shim(container, "chat-abort", ["--session", SESSION]);
    shim(container, "session-reset", ["--session", SESSION]);
  });

  // ── Toolchain / layout ────────────────────────────────────────────────

  describe("install", () => {
    it("ships bun, node 22 and the Claude Code CLI at upstream's hardcoded path", () => {
      const bun = execAsUser(container!, "bun --version");
      expect(bun.exitCode, bun.stderr).toBe(0);
      expect(bun.stdout.trim()).toMatch(/^\d+\.\d+\.\d+/);

      const node = execAsUser(container!, "node --version");
      expect(node.exitCode).toBe(0);
      expect(node.stdout.trim()).toMatch(/^v22\./);

      // src/providers/claude.ts passes pathToClaudeCodeExecutable: '/pnpm/claude'.
      const claude = execAsUser(container!, "/pnpm/claude --version");
      expect(claude.exitCode, claude.stderr).toBe(0);
      expect(claude.stdout).toMatch(/\d+\.\d+\.\d+/);
    });

    it("lays out /app and /workspace/agent the way the agent-runner expects", () => {
      expect(exec(container!, ["readlink", "-f", "/app/src"]).stdout.trim()).toBe(
        "/opt/nanoclaw/container/agent-runner/src",
      );
      for (const p of ["/app/node_modules", "/app/skills", "/app/CLAUDE.md"]) {
        expect(exec(container!, ["test", "-e", p]).exitCode, p).toBe(0);
      }
      expect(exec(container!, ["readlink", "/workspace/agent"]).stdout.trim()).toBe(WORKSPACE);
      expect(owner(container!, "/opt/nanoclaw")).toBe("claworc:claworc");
    });

    it("carries the session-dir build patch", () => {
      // patches/0001-session-dir-env.patch — without it every runner would
      // share the fixed /workspace mount point.
      const r = exec(container!, [
        "grep", "-q", "NANOCLAW_SESSION_DIR",
        "/opt/nanoclaw/container/agent-runner/src/db/connection.ts",
      ]);
      expect(r.exitCode).toBe(0);
    });

    it("shim can import the pinned NanoClaw DB schema", () => {
      // shimlib.mjs imports INBOUND_SCHEMA/OUTBOUND_SCHEMA from the upstream
      // host tree; a NANOCLAW_VERSION bump that moves that file breaks every
      // session-touching verb. Cheapest guard: load it the same way.
      const r = execAsUser(
        container!,
        `bun -e "const m = await import('/opt/nanoclaw/src/db/schema.ts'); if (typeof m.INBOUND_SCHEMA !== 'string' || typeof m.OUTBOUND_SCHEMA !== 'string') process.exit(1)"`,
      );
      expect(r.exitCode, r.stderr).toBe(0);
    });

    it("strips the SUID bit from su", () => {
      expect(mode(container!, "/usr/bin/su")).not.toMatch(/^4/);
    });
  });

  // ── Boot state ────────────────────────────────────────────────────────

  describe("boot state", () => {
    it("seeds the workspace and applies the boot LLM routing to container.json", () => {
      // configure-llm ran first (init-setup) from /defaults/container.json,
      // so the non-managed defaults must have survived alongside the model.
      expect(readJson(CONFIG)).toEqual({
        provider: "claude",
        model: DEFAULT_MODEL,
        assistantName: "NanoClaw",
        maxMessagesPerPrompt: 10,
        mcpServers: {},
      });
      // CLAUDE.md is only written by init-agent-seed.
      expect(exec(container!, ["test", "-s", `${WORKSPACE}/CLAUDE.md`]).exitCode).toBe(0);
      for (const p of [WORKSPACE, CONFIG, `${WORKSPACE}/CLAUDE.md`, STATE_DIR, RUN_DIR, AGENT_LOG]) {
        expect(owner(container!, p), p).toBe("claworc:claworc");
      }
    });

    it("writes the managed llm.json from CLAWORC_INITIAL_LLM_CONFIG", () => {
      expect(readJson(LLM_JSON)).toEqual(nanoclawLlmJson(BOOT_DOC));
      expect(mode(container!, LLM_JSON)).toBe("644");
      expect(owner(container!, LLM_JSON)).toBe("claworc:claworc");
    });

    it("runs the svc-agent supervisor as claworc with a fresh heartbeat", () => {
      expect(svstat("up")).toBe("true");
      const pid = svstat("pid");
      expect(pid).toMatch(/^\d+$/);
      expect(exec(container!, ["stat", "-c", "%U", `/proc/${pid}`]).stdout.trim()).toBe("claworc");
      expect(exec(container!, ["ps", "-o", "args=", "-p", pid]).stdout).toContain("host.mjs");

      const age = exec(container!, [
        "sh", "-c", `echo $(( $(date +%s) - $(stat -c %Y ${RUN_DIR}/host.heartbeat) ))`,
      ]);
      expect(age.exitCode).toBe(0);
      expect(Number(age.stdout.trim())).toBeLessThanOrEqual(15);
      expect(readFile(container!, AGENT_LOG)).toContain("[shim-host]");
    });
  });

  // ── Shim contract (read-only battery) ─────────────────────────────────

  shimContractTests(container!, {
    role: "nanoclaw",
    expectedMeta: nanoclawMeta,
    // Same read as agent/nanoclaw/shim/meta (Dockerfile strips the leading v).
    versionCommand: "head -n1 /opt/claworc/shim/nanoclaw.version | tr -cd '0-9A-Za-z.-'",
    readOnlyFiles: [
      "nanoclaw.version",
      "lib/chat-abort.mjs",
      "lib/chat-send.mjs",
      "lib/configure-llm.mjs",
      "lib/host.mjs",
      "lib/session-reset.mjs",
      "lib/shimlib.mjs",
    ],
  });

  // ── Mutating verbs ────────────────────────────────────────────────────

  describe("config-set", () => {
    let snapshot = "";
    beforeEach(() => {
      snapshot = shim(container!, "config-get").stdout;
    });
    afterEach(() => {
      expect(shim(container!, "config-set", [], snapshot).exitCode).toBe(0);
    });

    it("round-trips container.json and keeps ownership", () => {
      const next = JSON.stringify({ ...JSON.parse(snapshot), vitestMarker: 1 }, null, 2) + "\n";
      expect(shim(container!, "config-set", [], next).exitCode).toBe(0);
      expect(shim(container!, "config-get").stdout).toBe(next);
      expect(owner(container!, CONFIG)).toBe("claworc:claworc");
      expect(mode(container!, CONFIG)).toBe("644");
    });

    it("rejects invalid JSON with a parseable exit-6 error and leaves the file untouched", () => {
      // Bun's error message contains quotes; config-set builds its {"error"}
      // with printf + tr, so this guards that the result is still valid JSON.
      const r = shim(container!, "config-set", [], '{"a": "b}\n');
      expectValidationError(r);
      expect(JSON.parse(r.stdout).error).toMatch(/^invalid JSON/);
      expect(readFile(container!, CONFIG)).toBe(snapshot);
    });
  });

  describe("configure-llm", () => {
    afterEach(() => {
      // Every test in this block rewrites llm.json / container.json; put the
      // boot routing back so later tests (chat, selftest) see it.
      applyLlm(BOOT_DOC);
      expect(readJson(LLM_JSON)).toEqual(nanoclawLlmJson(BOOT_DOC));
      expect(readJson(CONFIG).model).toBe(DEFAULT_MODEL);
    });

    const newDoc = {
      ...BOOT_DOC,
      default_model: "anthropic/claude-opus-5",
      fallback_models: ["anthropic/claude-haiku-4-5-20251001"],
      providers: [
        { key: "openai", api_key: "claworc-vk-wrong-dialect", api_type: "openai-completions" },
        { key: "anthropic", api_key: "claworc-vk-rotated", api_type: "anthropic-messages" },
      ],
    };

    it("rewrites llm.json and the managed container.json keys, preserving user keys", () => {
      const before = readJson(CONFIG);
      expect(
        shim(container!, "config-set", [], JSON.stringify({ ...before, vitestMarker: 1 })).exitCode,
      ).toBe(0);

      applyLlm(newDoc);
      // Picks the anthropic-dialect provider, not providers[0].
      expect(readJson(LLM_JSON)).toEqual(nanoclawLlmJson(newDoc));
      const cfg = readJson(CONFIG);
      expect(cfg.model).toBe("anthropic/claude-opus-5");
      expect(cfg.provider).toBe("claude");
      expect(cfg.vitestMarker).toBe(1);
      expect(cfg.maxMessagesPerPrompt).toBe(10);
      expect(owner(container!, LLM_JSON)).toBe("claworc:claworc");
      expect(owner(container!, CONFIG)).toBe("claworc:claworc");

      // Idempotent: a second run is a byte-identical no-op.
      const llmBytes = readFile(container!, LLM_JSON);
      const cfgBytes = readFile(container!, CONFIG);
      applyLlm(newDoc);
      expect(readFile(container!, LLM_JSON)).toBe(llmBytes);
      expect(readFile(container!, CONFIG)).toBe(cfgBytes);

      expect(shim(container!, "config-set", [], JSON.stringify(before, null, 2) + "\n").exitCode).toBe(0);
    });

    it("rejects the openai dialect with exit 6 and leaves both files untouched", () => {
      const llmBytes = readFile(container!, LLM_JSON);
      const cfgBytes = readFile(container!, CONFIG);
      expectValidationError(shim(container!, "configure-llm", [], JSON.stringify({ ...BOOT_DOC, style: "openai" })));
      expect(readFile(container!, LLM_JSON)).toBe(llmBytes);
      expect(readFile(container!, CONFIG)).toBe(cfgBytes);
    });

    it("removes routing when proxy_url is empty", () => {
      applyLlm({ ...BOOT_DOC, proxy_url: "", providers: [], default_model: "" });
      expect(exec(container!, ["test", "-e", LLM_JSON]).exitCode).not.toBe(0);
      const cfg = readJson(CONFIG);
      expect(cfg.model).toBeUndefined();
      expect(cfg.provider).toBe("claude");
    });
  });

  it("restart recycles the supervisor and health recovers", { timeout: 90_000 }, async () => {
    const oldPid = svstat("pid");
    expect(shim(container!, "restart").exitCode).toBe(0);
    // The heartbeat file's mtime stays fresh across a quick restart, so a
    // bare health check would pass immediately — require a new PID too.
    const recycled = await waitFor(() => {
      const r = exec(container!, ["/command/s6-svstat", "-o", "up,pid", SVC]);
      if (r.exitCode !== 0) return false;
      const [up, pid] = r.stdout.trim().split(/\s+/);
      return up === "true" && pid !== oldPid && shim(container!, "health").exitCode === 0;
    }, 60_000, 1_000);
    if (!recycled) dumpDiagnostics(container!);
    expect(recycled).toBe(true);
    expect(exec(container!, ["stat", "-c", "%U", `/proc/${svstat("pid")}`]).stdout.trim()).toBe("claworc");
  });

  // ── Chat (no LLM reachable) ───────────────────────────────────────────

  describe("chat-send", () => {
    it("runs a turn through the session-DB contract even with the proxy unreachable", { timeout: 330_000 }, () => {
      // The exec timeout must exceed chat-send's RUNNER_GRACE_MS (120 s) so a
      // "runner never started" diagnostic end event is captured, not cut off.
      const r = shim(container!, "chat-send", ["--session", SESSION, "--turn", "t-hello"], "hello", 300_000);
      if (r.exitCode !== 0) console.error(r.stdout, r.stderr);
      expect(r.exitCode).toBe(0);
      const events = parseJsonl(r.stdout);
      assertChatContract(events, SESSION, "t-hello");
      // Only the runner reaching the SDK proves the whole path (supervisor
      // → runner spawn → inbound.db → processing_ack); the unreachable proxy
      // may surface as an error end or as an error-text reply, so stop_reason
      // is deliberately not pinned. These two must never be the reason:
      const codes = events.filter((e) => e.event === "error").map((e) => e.code);
      expect(codes).not.toContain("runner_not_started");
      expect(codes).not.toContain("agent_service_down");

      const dir = `${SESSIONS_DIR}/${SESSION}`;
      for (const f of ["inbound.db", "outbound.db"]) {
        expect(exec(container!, ["test", "-s", `${dir}/${f}`]).exitCode, f).toBe(0);
        expect(owner(container!, `${dir}/${f}`), f).toBe("claworc:claworc");
      }
      expect(readFile(container!, AGENT_LOG)).toContain(`runner spawned for session '${SESSION}'`);
    });

    it("session-reset drops the session directory and runner, idempotently", () => {
      expect(shim(container!, "session-reset", ["--session", SESSION]).exitCode).toBe(0);
      expect(exec(container!, ["test", "-e", `${SESSIONS_DIR}/${SESSION}`]).exitCode).not.toBe(0);
      expect(exec(container!, ["test", "-e", `${RUN_DIR}/runner-${SESSION}.pid`]).exitCode).not.toBe(0);
      expect(exec(container!, ["pgrep", "-f", `[N]ANOCLAW_SESSION_DIR=${SESSIONS_DIR}/${SESSION}`]).exitCode).not.toBe(0);
      expect(shim(container!, "session-reset", ["--session", SESSION]).exitCode).toBe(0);
    });
  });

  // ── Last: conformance script (mutates config, leaves a warm runner) ───

  it("passes the image's shim-selftest", { timeout: 420_000 }, () => {
    runShimSelftest(container!);
  });
});
