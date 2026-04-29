import { test, expect } from "../utils/base";
import { readSharedInstance } from "../utils/sharedInstance";
import * as path from "node:path";

const fixturesDir = path.resolve(process.cwd(), "fixtures");

test("file manager: mounts and accepts an upload", async ({ page }) => {
  test.setTimeout(2 * 60_000);
  const shared = readSharedInstance();
  test.skip(!shared, "shared instance not available (globalSetup did not run)");

  await page.goto(`/instances/${shared!.id}#files`);

  // SVAR Filemanager mounts inside a wrapper div; wait for at least one
  // file row or empty-state to render via the SVAR-emitted DOM.
  await expect(page.locator("body")).toBeVisible();

  // The SVAR Willow theme prefixes its DOM with `wx-`. Anything inside the
  // file-browser container counts as "mounted".
  await expect(page.locator(".wx-filemanager, .wx-list, .wx-table").first()).toBeVisible({
    timeout: 30_000,
  });

  // Set the file directly on the hidden upload input.
  const fileName = `hello-${Date.now()}.txt`;
  const tmpFile = path.join(fixturesDir, "hello.txt");
  // Rename in-flight by writing a fresh file alongside is overkill — use the
  // fixture path; instead assert by partial match on `hello`.
  await page.locator('input[type="file"]').first().setInputFiles(tmpFile);

  // After upload, the SVAR list should contain a row labelled `hello.txt`.
  await expect(page.getByText("hello.txt").first()).toBeVisible({ timeout: 30_000 });
  // Silence unused-var warning for fileName (kept for future use if we want
  // to write a unique-named fixture before uploading).
  void fileName;
});
