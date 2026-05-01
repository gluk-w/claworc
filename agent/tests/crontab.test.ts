import { describe, it, expect } from "vitest";
import { exec, getContainers, hasCommand } from "./helpers";

const containers = getContainers();

// Only the claworc-agent image runs cron; the browser-only images
// (claworc-browser-*) deliberately omit it. Probe each container and
// skip the ones without a cron binary so the suite stays green against
// the split images.
const entries = Object.entries(containers)
  .map(([browser, info]) => [browser, info.name] as [string, string])
  .filter(([, name]) => hasCommand(name, "cron"));

if (entries.length === 0) {
  describe.skip("cron (no cron-capable containers available)", () => {
    it.skip("skipped — no container ships cron", () => {});
  });
}

describe.skipIf(entries.length === 0).each(entries)(
  "cron: %s",
  (_browser, container) => {
    it("cron process is running", () => {
      const result = exec(container, ["pgrep", "-x", "cron"]);
      expect(result.exitCode).toBe(0);
      expect(result.stdout.trim()).not.toBe("");
    });
  },
);
