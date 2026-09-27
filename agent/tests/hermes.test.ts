/**
 * Integration tests for the claworc/hermes agent image (agent/hermes/).
 *
 * Hermes Agent (NousResearch/hermes-agent) is a Python CLI; the image ships
 * no agent daemon — each chat turn spawns `hermes chat --query … -Q` via the
 * shim's chat-send verb. What we verify here:
 *   - the pinned install and the seeded ~/.hermes layout,
 *   - the CLAWORC_INITIAL_LLM_CONFIG boot path (init-agent-seed →
 *     configure-llm) landed in config.yaml's claworc-managed block,
 *   - every shim verb per docs/shim.md, including validation and idempotency,
 *   - the chat JSONL contract with no LLM reachable (port 40001 is closed).
 *
 * Ordering matters: boot-state assertions first, mutating verbs after, and
 * shim-selftest + the destructive reseed check last.
 */
import { describe, it, expect, beforeAll, beforeEach, afterEach, afterAll } from "vitest";
import {
  exec,
  execAsUser,
  execInput,
  getContainers,
  dumpDiagnostics,
  parseJsonl,
  shim,
  waitFor,
} from "./helpers";
import { llmConfigFor, hermesManagedBlock, hermesMeta, PROXY_URL, DEFAULT_MODEL } from "./fixtures";
import {
  expectValidationError,
  owner,
  mode,
  readFile,
  runShimSelftest,
  shimContractTests,
} from "./shim-contract";

const containers = getContainers();
const container = containers.hermes?.name;

const HERMES_HOME = "/home/claworc/.hermes";
const CONFIG = `${HERMES_HOME}/config.yaml`;
const ENV_FILE = `${HERMES_HOME}/.env`;
const VENV_PY = "/opt/hermes/venv/bin/python";
const BOOT_DOC = llmConfigFor("hermes");
const BOOT_KEY = BOOT_DOC.providers[0].api_key;
const SESSION = "vitest";

/** Parse a YAML document with the PyYAML inside the Hermes venv (read-only). */
function parseYaml(text: string): any {
  const r = execInput(
    container!,
    [VENV_PY, "-c", "import sys,yaml,json; print(json.dumps(yaml.safe_load(sys.stdin.read())))"],
    text,
  );
  expect(r.exitCode, r.stderr).toBe(0);
  return JSON.parse(r.stdout);
}

function countLines(text: string, needle: string): number {
  return text.split("\n").filter((l) => l.trim() === needle).length;
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

describe.skipIf(!container)("hermes image", { timeout: 120_000 }, () => {
  beforeAll(async () => {
    // svc-sshd only depends on init-setup, so global-setup's sshd gate can
    // pass before init-agent-seed has seeded ~/.hermes and applied
    // CLAWORC_INITIAL_LLM_CONFIG. Wait for the boot virtual key to land in
    // config.yaml, then for health (which also warms Python's import path).
    const seeded = await waitFor(
      () => exec(container!, ["grep", "-q", BOOT_KEY, CONFIG]).exitCode === 0,
      300_000,
    );
    if (!seeded) {
      dumpDiagnostics(container!);
      throw new Error("hermes: boot LLM routing never landed in config.yaml");
    }
    const healthy = await waitFor(() => shim(container!, "health").exitCode === 0, 300_000, 5_000);
    if (!healthy) {
      dumpDiagnostics(container!);
      throw new Error("hermes: shim health never returned 0");
    }
  }, 960_000);

  afterAll(() => {
    // A timed-out `docker exec` does not kill the in-container chat-send.
    if (container) shim(container, "chat-abort", ["--session", SESSION]);
  });

  // ── Image layout / boot state ─────────────────────────────────────────

  describe("install", () => {
    it("hermes on PATH resolves into the pinned venv", () => {
      const r = exec(container!, ["sh", "-c", 'readlink -f "$(command -v hermes)"']);
      expect(r.exitCode).toBe(0);
      expect(r.stdout.trim()).toBe("/opt/hermes/venv/bin/hermes");
    });

    it("bakes the pinned version and answers --version as claworc", () => {
      expect(readFile(container!, "/opt/hermes/VERSION").trim()).not.toBe("");
      // Never run the hermes CLI as root: it would leave root-owned state
      // under ~/.hermes and break the claworc user.
      const r = execAsUser(container!, "hermes --version");
      expect(r.exitCode, r.stderr).toBe(0);
      expect(r.stdout).toMatch(/\d+\.\d+/);
    });

    it("venv python satisfies Hermes' requires-python (>=3.11)", () => {
      const r = exec(container!, [VENV_PY, "-c", "import sys; print(sys.version_info[0], sys.version_info[1])"]);
      expect(r.exitCode).toBe(0);
      const [major, minor] = r.stdout.trim().split(" ").map(Number);
      expect(major).toBe(3);
      expect(minor).toBeGreaterThanOrEqual(11);
    });

    it("has no agent daemon service (chat turns spawn the CLI)", () => {
      expect(exec(container!, ["test", "-d", "/run/service/svc-agent"]).exitCode).not.toBe(0);
      expect(exec(container!, ["test", "-d", "/run/service/svc-sshd"]).exitCode).toBe(0);
    });

    it("strips the SUID bit from su", () => {
      expect(mode(container!, "/usr/bin/su")).not.toMatch(/^4/);
    });
  });

  describe("seeded ~/.hermes", () => {
    it("home, workspace, shim state and log are owned by claworc", () => {
      for (const p of [HERMES_HOME, CONFIG, ENV_FILE, "/home/claworc/hermes-workspace", "/home/claworc/.claworc/shim", "/var/log/claworc/agent.log"]) {
        expect(owner(container!, p), p).toBe("claworc:claworc");
      }
      expect(mode(container!, CONFIG)).toBe("644");
      expect(mode(container!, ENV_FILE)).toBe("644");
    });

    it("config.yaml carries the boot LLM routing in the managed block", () => {
      const text = readFile(container!, CONFIG);
      expect(countLines(text, "# BEGIN claworc-managed")).toBe(1);
      expect(countLines(text, "# END claworc-managed")).toBe(1);
      const cfg = parseYaml(text);
      expect(cfg.security.tirith_enabled).toBe(false);
      expect(cfg.model).toEqual({
        provider: "custom",
        base_url: PROXY_URL,
        api_key: BOOT_KEY,
        default: DEFAULT_MODEL,
      });
    });

    it("config.yaml managed block is byte-exact", () => {
      const text = readFile(container!, CONFIG);
      expect(text).toContain(hermesManagedBlock(BOOT_DOC).join("\n"));
    });
  });

  // ── Shim contract (read-only battery) ─────────────────────────────────

  shimContractTests(container!, {
    role: "hermes",
    expectedMeta: hermesMeta,
    // Same read as agent/hermes/shim/meta.
    versionCommand: "head -n1 /opt/hermes/VERSION | tr -d '\"\\\\'",
    executableExtras: ["lib/ensure-seed.sh"],
  });

  it("config-get --id env returns .env bytes", () => {
    const r = shim(container!, "config-get", ["--id", "env"]);
    expect(r.exitCode).toBe(0);
    expect(r.stdout).toBe(readFile(container!, ENV_FILE));
  });

  it("restart is a contract-legal no-op", () => {
    expect(shim(container!, "restart").exitCode).toBe(0);
  });

  // ── Mutating verbs (snapshot + restore around each test) ──────────────

  describe("config-set", () => {
    let configSnapshot = "";
    let envSnapshot = "";
    beforeEach(() => {
      configSnapshot = shim(container!, "config-get").stdout;
      envSnapshot = shim(container!, "config-get", ["--id", "env"]).stdout;
    });
    afterEach(() => {
      expect(shim(container!, "config-set", [], configSnapshot).exitCode).toBe(0);
      expect(shim(container!, "config-set", ["--id", "env"], envSnapshot).exitCode).toBe(0);
    });

    it("round-trips config.yaml byte-for-byte and keeps ownership", () => {
      const next = configSnapshot + "# vitest marker\n";
      expect(shim(container!, "config-set", [], next).exitCode).toBe(0);
      expect(shim(container!, "config-get").stdout).toBe(next);
      expect(owner(container!, CONFIG)).toBe("claworc:claworc");
      expect(mode(container!, CONFIG)).toBe("644");
    });

    it("rejects invalid YAML with exit 6 and leaves the file untouched", () => {
      const r = shim(container!, "config-set", [], "foo: [unclosed\n");
      expectValidationError(r);
      expect(JSON.parse(r.stdout).error).toMatch(/^invalid YAML/);
      expect(readFile(container!, CONFIG)).toBe(configSnapshot);
    });

    it("round-trips .env and rejects non KEY=VALUE lines", () => {
      const next = envSnapshot + "FOO=bar\nexport BAZ=qux\n";
      expect(shim(container!, "config-set", ["--id", "env"], next).exitCode).toBe(0);
      expect(readFile(container!, ENV_FILE)).toBe(next);

      const bad = shim(container!, "config-set", ["--id", "env"], "this is not kv\n");
      expectValidationError(bad);
      expect(readFile(container!, ENV_FILE)).toBe(next);
    });
  });

  describe("configure-llm", () => {
    let configSnapshot = "";
    beforeEach(() => {
      configSnapshot = shim(container!, "config-get").stdout;
    });
    afterEach(() => {
      expect(shim(container!, "config-set", [], configSnapshot).exitCode).toBe(0);
    });

    const newDoc = {
      ...BOOT_DOC,
      default_model: "openai/gpt-5",
      providers: [{ key: "openai", api_key: "claworc-vk-rotated", api_type: "openai-completions" }],
    };

    it("rewrites only the managed block and is idempotent", () => {
      // Put a user line outside the block so we can prove it survives.
      const withMarker = configSnapshot + "# vitest user comment\n";
      expect(shim(container!, "config-set", [], withMarker).exitCode).toBe(0);

      const first = shim(container!, "configure-llm", [], JSON.stringify(newDoc));
      expect(first.exitCode, first.stdout + first.stderr).toBe(0);
      const afterFirst = readFile(container!, CONFIG);
      expect(afterFirst).toContain(hermesManagedBlock(newDoc).join("\n"));
      expect(afterFirst).toContain("# vitest user comment");
      expect(countLines(afterFirst, "# BEGIN claworc-managed")).toBe(1);
      expect(countLines(afterFirst, "# END claworc-managed")).toBe(1);
      const cfg = parseYaml(afterFirst);
      expect(cfg.security.tirith_enabled).toBe(false);
      expect(cfg.model.default).toBe("openai/gpt-5");
      expect(cfg.model.api_key).toBe("claworc-vk-rotated");
      expect(owner(container!, CONFIG)).toBe("claworc:claworc");

      expect(shim(container!, "configure-llm", [], JSON.stringify(newDoc)).exitCode).toBe(0);
      expect(readFile(container!, CONFIG)).toBe(afterFirst);
    });

    it("rejects the anthropic dialect with exit 6 and leaves config untouched", () => {
      const r = shim(container!, "configure-llm", [], JSON.stringify({ ...BOOT_DOC, style: "anthropic" }));
      expectValidationError(r);
      expect(readFile(container!, CONFIG)).toBe(configSnapshot);
    });
  });

  // ── Chat (no LLM reachable) ───────────────────────────────────────────

  describe("chat-send", () => {
    it("turns an empty message into a fatal error end without spawning hermes", () => {
      const r = shim(container!, "chat-send", ["--session", SESSION, "--turn", "t-empty"], "   \n");
      expect(r.exitCode, r.stderr).toBe(0);
      const events = parseJsonl(r.stdout);
      assertChatContract(events, SESSION, "t-empty");
      expect(events.map((e) => e.event)).toEqual(["start", "error", "end"]);
      expect(events[1].code).toBe("empty_message");
      expect(events[1].fatal).toBe(true);
      expect(events[2].stop_reason).toBe("error");
    });

    it("streams a well-formed turn even when the LLM proxy is unreachable", { timeout: 330_000 }, () => {
      const r = shim(container!, "chat-send", ["--session", SESSION, "--turn", "t-hello"], "hello", 300_000);
      if (r.exitCode !== 0) console.error(r.stdout, r.stderr);
      expect(r.exitCode).toBe(0);
      const events = parseJsonl(r.stdout);
      assertChatContract(events, SESSION, "t-hello");
      // Nothing listens on PROXY_URL, so we pin the contract shape only —
      // not whether Hermes surfaces that as an error end or as reply text.
      expect(readFile(container!, "/var/log/claworc/agent.log")).toContain("[chat-send]");
      expect(owner(container!, HERMES_HOME)).toBe("claworc:claworc");
    });
  });

  // ── Last: conformance script, then the destructive reseed check ───────

  it("passes the image's shim-selftest", { timeout: 420_000 }, () => {
    runShimSelftest(container!);
  });

  it("verbs lazily reseed ~/.hermes from the baked skeleton", () => {
    // Covers containers that never ran the s6 boot sequence (selftest,
    // `docker run --entrypoint sh`). Destroys instance state — keep last.
    expect(exec(container!, ["rm", "-rf", HERMES_HOME]).exitCode).toBe(0);
    const r = shim(container!, "config-get");
    expect(r.exitCode, r.stderr).toBe(0);
    expect(r.stdout).toBe(readFile(container!, "/opt/hermes-skeleton/.hermes/config.yaml"));
    expect(owner(container!, HERMES_HOME)).toBe("claworc:claworc");
    expect(owner(container!, CONFIG)).toBe("claworc:claworc");
    expect(shim(container!, "health").exitCode).toBe(0);
  });
});
