import { test as base, expect } from "@playwright/test";
import { readSharedInstance, SharedInstanceState } from "./sharedInstance";
import type { AgentType } from "./e2eConfig";

type Fixtures = {
  // The shared instance globalSetup created for this project's agent type.
  // Skips the test when it is missing (globalSetup failed or the type was
  // excluded via E2E_AGENT_TYPES).
  shared: SharedInstanceState;
};

type Options = {
  // Set per Playwright project (playwright.config.ts): which agent type the
  // per-agent suites exercise.
  agentType: AgentType;
};

// Background task toasts (react-hot-toast `[data-rht-toaster]`) replay from
// server-side task retention via SSE on every page load. When prior tests have
// created tasks (e.g. instance.create), the resulting overlay can intercept
// clicks on sticky action bars in unrelated specs. We inject a CSS rule into
// every page to keep them out of the test's way; this only affects test
// browsers, never real users.
export const test = base.extend<Fixtures & Options>({
  agentType: ["openclaw", { option: true }],
  shared: async ({ agentType }, use, testInfo) => {
    const shared = readSharedInstance(agentType);
    testInfo.skip(!shared, `shared ${agentType} instance not available (globalSetup did not create it)`);
    await use(shared!);
  },
  page: async ({ page }, use) => {
    await page.addInitScript(() => {
      const style = document.createElement("style");
      style.textContent = `
        [data-rht-toaster],
        [data-rht-toaster] * {
          display: none !important;
          pointer-events: none !important;
        }
      `;
      const apply = () => document.head?.appendChild(style);
      if (document.head) apply();
      else document.addEventListener("DOMContentLoaded", apply);
    });
    await use(page);
  },
});

export { expect };
