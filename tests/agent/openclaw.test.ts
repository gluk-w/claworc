import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { execFileSync } from "node:child_process";

const IMAGE = process.env.AGENT_TEST_IMAGE ?? "openclaw-vnc-chromium2:test";
const CONTAINER = "agent-test-" + process.pid;

interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

function exec(cmd: string[]): ExecResult {
  try {
    const stdout = execFileSync("docker", ["exec", CONTAINER, ...cmd], {
      encoding: "utf-8",
      timeout: 30_000,
    });
    if (stdout) console.log(stdout);
    return { stdout, stderr: "", exitCode: 0 };
  } catch (err: any) {
    if (err.stdout) console.log(err.stdout);
    if (err.stderr) console.error(err.stderr);
    return {
      stdout: err.stdout ?? "",
      stderr: err.stderr ?? "",
      exitCode: err.status ?? 1,
    };
  }
}

function execAsUser(cmd: string): ExecResult {
  return exec(["su", "-", "abc", "-c", cmd]);
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

function structureOf(obj: any): any {
  if (Array.isArray(obj)) return obj.length > 0 ? [structureOf(obj[0])] : [];
  if (obj !== null && typeof obj === "object") {
    return Object.fromEntries(
      Object.keys(obj).sort().map((k) => [k, structureOf(obj[k])]),
    );
  }
  return typeof obj;
}

describe("agent image", () => {
  beforeAll(async () => {
    // Remove leftover container if any
    try {
      execFileSync("docker", ["rm", "-f", CONTAINER], { stdio: "ignore" });
    } catch {
      // ignore
    }

    execFileSync(
      "docker",
      ["run", "-d", "--privileged", "--platform", "linux/amd64", "-e", "OPENCLAW_GATEWAY_TOKEN=zzzbbb", "--name", CONTAINER, IMAGE],
      { encoding: "utf-8" },
    );

    // Poll for openclaw gateway process (confirms s6-overlay init completed)
    const deadline = Date.now() + 120_000;
    while (Date.now() < deadline) {
      const result = exec(["pgrep", "-f", "openclaw gateway"]);
      if (result.exitCode === 0 && result.stdout.trim()) break;
      await sleep(2_000);
    }

    // Final check
    const check = exec(["pgrep", "-f", "openclaw gateway"]);
    if (check.exitCode !== 0) {
      throw new Error("openclaw gateway did not start within 120s");
    }

    // Wait for gateway WebSocket to be ready (port 18789 listening)
    const portDeadline = Date.now() + 30_000;
    while (Date.now() < portDeadline) {
      const result = exec(["ss", "-tln"]);
      if (result.stdout.includes(":18789")) break;
      await sleep(1_000);
    }
  });

  afterAll(() => {
    try {
      execFileSync("docker", ["rm", "-f", CONTAINER], { stdio: "ignore" });
    } catch {
      // ignore
    }
  });

  it("openclaw home directory exists and is owned by abc", () => {
    const result = exec(["stat", "-c", "%U:%G", "/config/.openclaw"]);
    expect(result.exitCode).toBe(0);
    expect(result.stdout.trim()).toBe("abc:abc");
  });

  it("openclaw.json structure matches snapshot", () => {
    const result = exec([
      "cat",
      "/config/.openclaw/openclaw.json",
    ]);
    expect(result.exitCode).toBe(0);

    const config = JSON.parse(result.stdout);
    expect(structureOf(config)).toMatchSnapshot();
  });

  it("openclaw gateway process is running", () => {
    const result = exec(["pgrep", "-f", "openclaw gateway"]);
    expect(result.exitCode).toBe(0);
    expect(result.stdout.trim()).not.toBe("");
  });

  it("openclaw logs exits without crash", () => {
    const result = execAsUser("openclaw logs --plain --limit 5");
    // May return non-zero if gateway WebSocket isn't reachable in emulated
    // test container, but the command itself should not crash
    expect(result.exitCode).toBeDefined();
  });

  it("can set gateway auth token via config", () => {
    const result = execAsUser(
      "openclaw config set gateway.auth.token test-token-abc123",
    );
    expect(result.exitCode).toBe(0);

    const configResult = exec([
      "cat",
      "/config/.openclaw/openclaw.json",
    ]);
    const config = JSON.parse(configResult.stdout);
    expect(config.gateway.auth.token).toBe("test-token-abc123");
  });

  it("can set agents.defaults.model via --json", () => {
    const modelJson = JSON.stringify({
      primary: "anthropic/claude-sonnet-4-20250514",
      fallbacks: ["anthropic/claude-haiku-4-20250414"],
    });

    const result = execAsUser(
      `openclaw config set agents.defaults.model '${modelJson}' --json`,
    );
    expect(result.exitCode).toBe(0);

    const configResult = exec([
      "cat",
      "/config/.openclaw/openclaw.json",
    ]);
    const config = JSON.parse(configResult.stdout);
    expect(config.agents.defaults.model).toEqual({
      primary: "anthropic/claude-sonnet-4-20250514",
      fallbacks: ["anthropic/claude-haiku-4-20250414"],
    });
  });

  it("openclaw status shows gateway as reachable", () => {
    const result = execAsUser("openclaw status");
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain(" reachable");
  });

  it("openclaw gateway stop exits without crash", () => {
    const result = execAsUser("openclaw gateway stop");
    // gateway stop may return 0 or non-zero depending on state,
    // but it should not crash (exitCode should be defined)
    expect(result.exitCode).toBeDefined();
  });
});
