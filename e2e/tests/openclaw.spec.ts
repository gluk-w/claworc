import { test, expect } from "../utils/base";
import { readSharedInstance } from "../utils/sharedInstance";

test("openclaw chat panel mounts and exposes input + send", async ({ page }) => {
  test.setTimeout(2 * 60_000);
  const shared = readSharedInstance();
  test.skip(!shared, "shared instance not available (globalSetup did not run)");

  await page.goto(`/instances/${shared!.id}#chat`);

  // The textarea is always rendered; only its placeholder and disabled state
  // depend on websocket connection. Checking presence + the Send button is a
  // robust assertion that the chat panel mounted and is wired up.
  const textarea = page.locator("textarea").first();
  await expect(textarea).toBeVisible({ timeout: 30_000 });

  const send = page.locator('button[title="Send"]');
  await expect(send).toBeVisible();
});
