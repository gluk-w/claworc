import { test, expect } from "../../utils/base";
import { apiClient, deleteInstance, listInstances } from "../../utils/api";
import { imageOverride } from "../../utils/e2eConfig";
import { waitForRunning } from "../../utils/agentUi";

test.describe("create agent from the dashboard", { tag: "@e2e" }, () => {
  test("create → running → delete", async ({ page, agentType }) => {
    test.setTimeout(8 * 60_000);
    const api = await apiClient();

    // The create form only exposes an image field for custom agents. When an
    // image override is configured (e.g. a locally built dev image), add it to
    // the form's request so the dashboard flow is exercised against it.
    const image = imageOverride(agentType);
    if (image) {
      await page.route("**/api/v1/instances", async (route) => {
        const req = route.request();
        if (req.method() !== "POST") return route.continue();
        const body = { ...req.postDataJSON(), container_image: image };
        await route.continue({ postData: JSON.stringify(body) });
      });
    }

    await page.goto("/instances/new");
    await expect(page.getByRole("heading", { name: "Create Agent", level: 1 })).toBeVisible();

    const displayName = `e2e-ui-${agentType}-${Date.now()}`;
    await page.getByTestId("display-name-input").fill(displayName);
    const typeButton = page.getByTestId(`agent-type-${agentType}`);
    await typeButton.click();
    await expect(typeButton).toHaveAttribute("aria-pressed", "true");

    const createReq = page.waitForRequest(
      (r) => r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/instances",
    );
    await page.getByTestId("create-instance-button").click();

    // Without models selected the form asks for confirmation; the agent still starts.
    const continueBtn = page.getByRole("button", { name: "Continue" });
    if (await continueBtn.isVisible({ timeout: 3_000 }).catch(() => false)) {
      await continueBtn.click();
    }
    expect((await createReq).postDataJSON()).toMatchObject({ agent_type: agentType });

    await page.waitForURL((url) => url.pathname === "/", { timeout: 30_000 });
    await expect(page.getByText(displayName).first()).toBeVisible({ timeout: 30_000 });

    const list: Array<{ id: number; display_name: string; agent_type: string }> = await listInstances(api);
    const created = list.find((i) => i.display_name === displayName);
    expect(created, `instance ${displayName} not found in list`).toBeTruthy();
    try {
      expect(created!.agent_type).toBe(agentType);
      await waitForRunning(api, created!.id, 6 * 60_000);
    } finally {
      await deleteInstance(api, created!.id);
    }

    const after: Array<{ id: number }> = await listInstances(api);
    expect(after.find((i) => i.id === created!.id)).toBeUndefined();
  });
});
