import { describe, it, expect, beforeAll } from "vitest";
import { exec, execAsUser, sleep, getContainers, dumpDiagnostics } from "./helpers";

const containers = getContainers();
// openclaw lives in the claworc-agent image only. Browser-only images
// (claworc-browser-*) don't ship the gateway, so this suite runs against
// the dedicated `agent` container launched by global-setup.ts when the
// instance image is available locally.
const container = containers.agent?.name;

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

  // chrome-data must be created by the desktop service (only when Chrome runs),
  // not by init-setup.sh. Otherwise on-demand-layout agents — where Chrome
  // lives in a separate browser pod — would still get a stale chrome-data/
  // visible in the file manager. init-setup.sh may legitimately *remove*
  // the dir on agent images (no svc-desktop), so this test only forbids
  // mkdir-style creation.
  it("init-setup.sh does not create chrome-data", () => {
    const result = exec(container!, [
      "grep",
      "-E",
      "mkdir.*chrome-data",
      "/etc/s6-overlay/scripts/init-setup.sh",
    ]);
    expect(result.exitCode).not.toBe(0);
  });

  // Conversely, on the agent image the init script must remove any leftover
  // chrome-data dir from a prior legacy boot so it isn't reachable from the
  // agent SSH/terminal/file-manager.
  it("init-setup.sh removes chrome-data when svc-desktop is absent", () => {
    const result = exec(container!, [
      "grep",
      "-E",
      "rm -rf /home/claworc/chrome-data",
      "/etc/s6-overlay/scripts/init-setup.sh",
    ]);
    expect(result.exitCode).toBe(0);
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

  it("openclaw status shows gateway is running", () => {
    const result = execAsUser(container!, "openclaw status");
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("ws://127.0.0.1:18789");
  });

  it("openclaw gateway stop exits without crash", () => {
    const result = execAsUser(container!, "openclaw gateway stop");
    expect(result.exitCode).toBeDefined();
  });

  // Regression for https://github.com/gluk-w/claworc/issues/127. sharp is a
  // native addon (libvips) used by openclaw's image pipeline (Telegram,
  // screenshots). It silently went missing when sharp 0.34 switched to
  // prebuilt binaries that need libvips on the system.
  describe("sharp image dependency (issue #127)", () => {
    const cdOpenclaw = 'cd "$(npm root -g)/openclaw"';

    it("openclaw still declares sharp as a build dependency", () => {
      // If upstream openclaw drops sharp, the runtime check below loses its
      // meaning — surface that change so we can revisit the Dockerfile.
      // openclaw lists sharp under pnpm's `onlyBuiltDependencies` rather
      // than `dependencies`/`optional`/`peer`, so check all four locations.
      const result = exec(container!, [
        "bash",
        "-c",
        `${cdOpenclaw} && node -e "const p=require('./package.json'); const inField=(o)=>o&&(o.sharp||(Array.isArray(o)&&o.includes('sharp'))); const found=inField(p.dependencies)||inField(p.optionalDependencies)||inField(p.peerDependencies)||inField(p.pnpm&&p.pnpm.onlyBuiltDependencies); if(!found){process.exit(2)} console.log('ok')"`,
      ]);
      expect(result.exitCode).toBe(0);
      expect(result.stdout.trim()).toBe("ok");
    });

    it("openclaw can load sharp for image processing", () => {
      // Resolve sharp the same way openclaw does at runtime — from its own
      // node_modules — so this fails loudly if libvips is missing or the
      // native binding didn't get built.
      const result = exec(container!, [
        "bash",
        "-c",
        `${cdOpenclaw} && node -e "console.log(require('sharp').versions.sharp)"`,
      ]);
      expect(result.exitCode).toBe(0);
      expect(result.stdout.trim()).toMatch(/^\d+\.\d+\.\d+/);
    });
  });
});
