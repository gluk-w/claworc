import { test, expect } from "../utils/base";
import { apiClient, deleteInstance, listInstances } from "../utils/api";
import { waitForInstanceStatus } from "../utils/waitForInstance";

test("create → run → delete instance lifecycle", async ({ page }) => {
  test.setTimeout(5 * 60_000);
  const api = await apiClient();

  await page.goto("/instances/new");
  await expect(page.getByRole("heading", { name: "Create Instance" })).toBeVisible();

  const displayName = `e2e-${Date.now()}`;
  await page.getByTestId("display-name-input").fill(displayName);
  await page.getByTestId("create-instance-button").click();

  // With no providers configured the form opens a "No models selected" warning.
  // Click "Continue" to proceed without models — the agent pod still starts.
  const continueBtn = page.getByRole("button", { name: "Continue" });
  if (await continueBtn.isVisible().catch(() => false)) {
    await continueBtn.click();
  }

  // After successful create, the page navigates back to the dashboard.
  await page.waitForURL((url) => url.pathname === "/", { timeout: 30_000 });

  // Look up the newly created instance via the API so we can poll its status.
  const list: Array<{ id: number; display_name: string }> = await listInstances(api);
  const created = list.find((i) => i.display_name === displayName);
  expect(created, `instance with display_name=${displayName} not found in list`).toBeTruthy();

  await waitForInstanceStatus(api, created!.id, "running", 4 * 60_000);

  await deleteInstance(api, created!.id);

  const after: Array<{ id: number }> = await listInstances(api);
  expect(after.find((i) => i.id === created!.id)).toBeUndefined();
});
