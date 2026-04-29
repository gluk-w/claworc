import { test, expect } from "../utils/base";
import { apiClient, getHealth } from "../utils/api";

test("dashboard loads and /health is ok", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });

  await page.goto("/");
  await expect(page).toHaveURL(/\/(?!login).*/);
  await expect(page.locator("body")).toBeVisible();

  const api = await apiClient();
  const health = await getHealth(api);
  expect(health.ok).toBe(true);

  expect(consoleErrors, `console errors:\n${consoleErrors.join("\n")}`).toEqual([]);
});
