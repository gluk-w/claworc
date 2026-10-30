import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { test, expect } from "../../utils/base";
import { apiClient } from "../../utils/api";
import { nonce, readInstanceFile } from "../../utils/agentUi";

test.describe("files", { tag: "@e2e" }, () => {
  test("upload through the file manager lands in the agent's home", async ({ page, shared }) => {
    test.setTimeout(2 * 60_000);
    const api = await apiClient();

    const name = `${nonce("upload")}.txt`;
    const content = `hello from the claworc e2e suite (${name})\n`;
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "claworc-e2e-"));
    const local = path.join(tmpDir, name);
    fs.writeFileSync(local, content);

    await page.goto(`/instances/${shared.id}#files`);
    // SVAR Filemanager (wx- prefixed DOM) is mounted once any list renders.
    await expect(page.locator(".wx-filemanager, .wx-list, .wx-table").first()).toBeVisible({
      timeout: 30_000,
    });

    await page.locator('input[type="file"]').first().setInputFiles(local);
    await expect(page.getByText(name).first()).toBeVisible({ timeout: 30_000 });

    // The file really exists in the container with the uploaded bytes.
    await expect.poll(() => readInstanceFile(api, shared.id, `/home/claworc/${name}`)).toBe(content);

    await api.delete(`/api/v1/instances/${shared.id}/files`, { params: { path: `/home/claworc/${name}` } });
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });
});
