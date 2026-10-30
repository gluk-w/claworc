import { defineConfig, devices } from "@playwright/test";
import type { AgentType } from "./utils/e2eConfig";

const baseURL = process.env.BASE_URL ?? "http://localhost:18001";
// E2E_BROWSER_CHANNEL=chrome runs the suites in the locally installed Google
// Chrome instead of Playwright's bundled Chromium.
const channel = process.env.E2E_BROWSER_CHANNEL || undefined;

// Per-agent suites (tests/agent/, tagged @e2e) run once per agent type, each
// against the instance globalSetup created for that type.
const agentProject = (agentType: AgentType) => ({
  name: agentType,
  testDir: "./tests/agent",
  use: { ...devices["Desktop Chrome"], channel, agentType },
});

export default defineConfig({
  testDir: "./tests",
  globalSetup: "./globalSetup.ts",
  globalTeardown: "./globalTeardown.ts",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      // Dashboard-wide specs that don't depend on an agent type.
      name: "dashboard",
      testIgnore: "agent/**",
      use: { ...devices["Desktop Chrome"], channel },
    },
    agentProject("openclaw"),
    agentProject("hermes"),
    agentProject("nanoclaw"),
  ],
});
