import { test, expect } from "../utils/base";
import { readSharedInstance } from "../utils/sharedInstance";

test("terminal: connects, executes a command, sees output", async ({ page }) => {
  test.setTimeout(2 * 60_000);
  const shared = readSharedInstance();
  test.skip(!shared, "shared instance not available (globalSetup did not run)");

  await page.goto(`/instances/${shared!.id}#terminal`);

  const xterm = page.locator(".xterm").first();
  await expect(xterm).toBeVisible({ timeout: 30_000 });

  // Wait for an interactive shell prompt — bash/zsh PS1 typically ends in `$ ` or `# `.
  await expect(xterm).toContainText(/[\$#]\s/, { timeout: 30_000 });

  await xterm.click();
  const marker = `claworc-e2e-${Date.now()}`;
  await page.keyboard.type(`echo ${marker}`);
  await page.keyboard.press("Enter");

  await expect(xterm).toContainText(marker, { timeout: 15_000 });
});
