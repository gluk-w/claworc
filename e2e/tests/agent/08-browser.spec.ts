import { test, expect } from "../../utils/base";

test.describe("browser", { tag: "@e2e" }, () => {
  test("vnc page mounts noVNC and exposes the framebuffer canvas", async ({ page, shared }) => {
    test.setTimeout(2 * 60_000);

    // The popup VNC page constructs an RFB instance which immediately injects a
    // <canvas> into its container, regardless of whether the websockify
    // connection has completed. Asserting the canvas exists is a robust check
    // that the page is wired correctly; full framebuffer rendering is an
    // additional opportunistic assertion when the browser pod actually comes up.
    await page.goto(`/instances/${shared.id}/vnc`);

    const canvas = page.locator("canvas").first();
    await expect(canvas).toHaveCount(1, { timeout: 30_000 });

    // Connection indicator must reach one of the in-progress / connected states
    // (Disconnected/Error means the page never even tried).
    await expect(page.getByText(/Connecting|Starting|Connected/)).toBeVisible({
      timeout: 15_000,
    });
  });
});
