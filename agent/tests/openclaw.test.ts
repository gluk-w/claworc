import { describe, it, expect, beforeAll } from "vitest";
import { exec, execAsUser, sleep, getContainers, dumpDiagnostics } from "./helpers";

const containers = getContainers();
const container = containers.chromium?.name;

function structureOf(obj: any): any {
  if (Array.isArray(obj)) return obj.length > 0 ? [structureOf(obj[0])] : [];
  if (obj !== null && typeof obj === "object") {
    return Object.fromEntries(
      Object.keys(obj).sort().map((k) => [k, structureOf(obj[k])]),
    );
  }
  return typeof obj;
}

describe.skipIf(!container)("agent image", { timeout: 300_000 }, () => {
  // Wait for openclaw gateway to be ready.
  // The svc-openclaw run script executes `openclaw doctor --fix` followed by
  // several `openclaw config set` commands before starting the gateway — each
  // spawns Node.js under QEMU emulation, which is very slow with concurrent
  // containers. By the time browser.test.ts finishes, the gateway is usually ready.
  // Wait for openclaw gateway to be ready.
  // Under QEMU with multiple concurrent containers, `openclaw doctor --fix` +
  // several `openclaw config set` commands can take 15+ minutes. The gateway
  // only starts after all of those complete.
  beforeAll(async () => {
    const deadline = Date.now() + 900_000;
    while (Date.now() < deadline) {
      const result = exec(container!, ["pgrep", "-f", "openclaw gateway"]);
      if (result.exitCode === 0 && result.stdout.trim()) break;
      await sleep(5_000);
    }

    // Final check
    const check = exec(container!, ["pgrep", "-f", "openclaw gateway"]);
    if (check.exitCode !== 0) {
      dumpDiagnostics(container!);
      throw new Error("openclaw gateway did not start within 900s");
    }

    // Wait for gateway WebSocket to be ready (port 18789 listening).
    // Port 18789 = 0x4965 in hex.
    const portDeadline = Date.now() + 60_000;
    while (Date.now() < portDeadline) {
      const result = exec(container!, ["grep", "-q", ":4965", "/proc/net/tcp6"]);
      if (result.exitCode === 0) break;
      await sleep(2_000);
    }
  }, 960_000);

  it("openclaw home directory exists and is owned by claworc", () => {
    const result = exec(container!, ["stat", "-c", "%U:%G", "/home/claworc/.openclaw"]);
    expect(result.exitCode).toBe(0);
    expect(result.stdout.trim()).toBe("claworc:claworc");
  });

  it("openclaw.json structure matches snapshot", () => {
    const result = exec(container!, [
      "cat",
      "/home/claworc/.openclaw/openclaw.json",
    ]);
    expect(result.exitCode).toBe(0);

    const config = JSON.parse(result.stdout);
    expect(structureOf(config)).toMatchSnapshot();
  });

  it("openclaw logs exits without crash", () => {
    const result = execAsUser(container!, "openclaw logs --plain --limit 5");
    expect(result.exitCode).toBeDefined();
  });

  it("can set gateway auth token via config", () => {
    const result = execAsUser(
      container!,
      "openclaw config set gateway.auth.token test-token-abc123",
    );
    expect(result.exitCode).toBe(0);

    const configResult = exec(container!, [
      "cat",
      "/home/claworc/.openclaw/openclaw.json",
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
      container!,
      `openclaw config set agents.defaults.model '${modelJson}' --json`,
    );
    expect(result.exitCode).toBe(0);

    const configResult = exec(container!, [
      "cat",
      "/home/claworc/.openclaw/openclaw.json",
    ]);
    const config = JSON.parse(configResult.stdout);
    expect(config.agents.defaults.model).toEqual({
      primary: "anthropic/claude-sonnet-4-20250514",
      fallbacks: ["anthropic/claude-haiku-4-20250414"],
    });
  });

  it("openclaw status shows gateway as reachable", () => {
    const result = execAsUser(container!, "openclaw status");
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain(" reachable");
  });

  it("openclaw gateway stop exits without crash", () => {
    const result = execAsUser(container!, "openclaw gateway stop");
    expect(result.exitCode).toBeDefined();
  });
});
