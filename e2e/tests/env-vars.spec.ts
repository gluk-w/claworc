import { test, expect } from "../utils/base";
import { readSharedInstance } from "../utils/sharedInstance";
import { SHARED_ENV_VARS } from "../utils/sharedEnv";

test("env vars set at create time are visible in the terminal", async ({ page }) => {
  test.setTimeout(2 * 60_000);
  const shared = readSharedInstance();
  test.skip(!shared, "shared instance not available (globalSetup did not run)");

  await page.goto(`/instances/${shared!.id}#terminal`);

  const xterm = page.locator(".xterm").first();
  await expect(xterm).toBeVisible({ timeout: 30_000 });
  await expect(xterm).toContainText(/[\$#]\s/, { timeout: 30_000 });

  await xterm.click();

  // Print each seeded env var inside a marker that's unlikely to appear in a
  // shell prompt or banner so we can scope the assertion to *this command's*
  // output rather than any leftover console text.
  for (const [name, value] of Object.entries(SHARED_ENV_VARS)) {
    const marker = `<<E2E:${name}>>`;
    await page.keyboard.type(`echo "${marker}$${name}${marker}"`);
    await page.keyboard.press("Enter");

    await expect(xterm).toContainText(`${marker}${value}${marker}`, {
      timeout: 15_000,
    });
  }
});
