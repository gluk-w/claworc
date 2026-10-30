import { test, expect } from "../../utils/base";
import { apiClient } from "../../utils/api";
import { SKILLS_DIR } from "../../utils/e2eConfig";
import { nonce, readInstanceFile } from "../../utils/agentUi";
import { makeZip } from "../../utils/zip";

test.describe("skills", { tag: "@e2e" }, () => {
  test("upload a skill to the library and install it on the agent", async ({ page, shared, agentType }) => {
    test.setTimeout(3 * 60_000);
    const api = await apiClient();

    const slug = nonce(`e2e-skill-${agentType}-`);
    const skillMd = [
      "---",
      `name: ${slug}`,
      "description: Trivial skill used by the Claworc E2E suite to verify upload and install.",
      "---",
      "",
      `When asked, reply with "${slug}-ok".`,
      "",
    ].join("\n");
    const helper = `#!/usr/bin/env bash\necho "${slug}-helper"\n`;
    const zip = makeZip({ "SKILL.md": skillMd, "scripts/helper.sh": helper });

    try {
      // Upload through the Skills page.
      await page.goto("/skills");
      await expect(page.getByRole("heading", { name: "Skills" })).toBeVisible();
      await page.getByRole("button", { name: /Upload Skill/ }).click();
      await page.locator('input[type="file"][accept=".zip"]').setInputFiles({
        name: `${slug}.zip`,
        mimeType: "application/zip",
        buffer: zip,
      });
      await page.getByRole("button", { name: "Upload", exact: true }).click();

      // The library card opens the deploy modal.
      const card = page.getByText(slug, { exact: true }).first();
      await expect(card).toBeVisible({ timeout: 15_000 });
      await card.click();
      await expect(page.getByRole("heading", { name: /Deploy .* to agents/ })).toBeVisible();

      const row = page.locator("div.rounded-lg.border", { hasText: shared.display_name }).last();
      await row.getByRole("checkbox").check();
      const deployRes = page.waitForResponse(
        (r) => r.request().method() === "POST" && r.url().includes(`/api/v1/skills/${slug}/deploy`),
      );
      await page.getByRole("button", { name: "Deploy to 1 agent" }).click();
      expect((await deployRes).ok()).toBe(true);
      // Deploys run as background tasks (progress shows in task toasts); the
      // modal closes once they are queued.
      await expect(page.getByRole("heading", { name: /Deploy .* to agents/ })).toHaveCount(0);

      // The shim's skill-install verb put the files into the agent's skills dir.
      const dir = `${SKILLS_DIR[agentType]}/${slug}`;
      await expect
        .poll(() => readInstanceFile(api, shared.id, `${dir}/SKILL.md`), { timeout: 90_000 })
        .toBe(skillMd);
      expect(await readInstanceFile(api, shared.id, `${dir}/scripts/helper.sh`)).toBe(helper);
    } finally {
      await api.delete(`/api/v1/skills/${slug}`);
    }
  });
});
