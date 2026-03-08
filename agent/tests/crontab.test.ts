import { describe, it, expect } from "vitest";
import { exec, getContainers } from "./helpers";

const containers = getContainers();

const entries = Object.entries(containers).map(
  ([browser, info]) => [browser, info.name] as [string, string],
);

// When no containers are available (no images built), skip all tests.
// We must have at least one describe block to avoid vitest "no test suite" error.
if (entries.length === 0) {
  describe.skip("cron (no containers available)", () => {
    it.skip("skipped — no agent images found", () => {});
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
