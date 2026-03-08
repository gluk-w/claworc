import { describe, it, expect } from "vitest";
import { exec, dumpDiagnostics, getContainers } from "./helpers";

const containers = getContainers();

const BROWSER_PROCESS_PATTERNS: Record<string, string> = {
  chromium: "chromium",
  chrome: "google-chrome",
  brave: "brave-browser",
};

// Build describe.each entries from containers launched by global-setup
const entries = Object.entries(containers).map(
  ([browser, info]) => [browser, info.name] as [string, string],
);

describe.skipIf(entries.length === 0).each(entries)(
  "browser: %s",
  (browser, container) => {
    it("browser process is running", () => {
      const pattern = BROWSER_PROCESS_PATTERNS[browser] ?? browser;
      const result = exec(container, ["pgrep", "-f", pattern]);
      expect(result.exitCode).toBe(0);
      expect(result.stdout.trim()).not.toBe("");
    });

    it("CDP port 9222 is available and responds", { timeout: 150_000 }, () => {
      const result = exec(container, [
        "curl",
        "-sf",
        "http://127.0.0.1:9222/json/version",
      ]);
      if (result.exitCode !== 0) {
        dumpDiagnostics(container);
      }
      expect(result.exitCode).toBe(0);
      const version = JSON.parse(result.stdout);
      expect(version).toHaveProperty("webSocketDebuggerUrl");
      expect(version).toHaveProperty("Browser");
    });

    it("CDP lists at least one page/target", () => {
      const result = exec(container, [
        "curl",
        "-sf",
        "http://127.0.0.1:9222/json/list",
      ]);
      expect(result.exitCode).toBe(0);
      const targets = JSON.parse(result.stdout);
      expect(Array.isArray(targets)).toBe(true);
      expect(targets.length).toBeGreaterThanOrEqual(1);
    });

    it("stealth extension is loaded", () => {
      const result = exec(container, [
        "ls",
        "/opt/stealth-extension/manifest.json",
      ]);
      expect(result.exitCode).toBe(0);
    });
  },
  300_000,
);
