import { test, expect } from "../utils/base";

test("Brave API key: change, save, masked, persists across reload", async ({ page }) => {
  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();

  const braveCard = page
    .locator("div", { has: page.getByRole("heading", { name: "Brave API Key" }) })
    .first();

  // Click "Change" to enter edit mode.
  await braveCard.getByRole("button", { name: "Change" }).click();

  const testKey = "test-brave-key-1234567890ABCD";
  const input = braveCard.getByPlaceholder("Enter Brave API key");
  await input.fill(testKey);

  // Global Save (sticky action bar at the bottom).
  await page.getByRole("button", { name: "Save", exact: true }).click();

  // Edit mode collapses on success → "Change" is visible again.
  await expect(braveCard.getByRole("button", { name: "Change" })).toBeVisible({ timeout: 15_000 });

  // Masked value shows last 4 chars.
  await expect(braveCard.getByText(/\*+ABCD/)).toBeVisible();

  // Persist across reload.
  await page.reload();
  await expect(page.getByText(/\*+ABCD/).first()).toBeVisible();
});
