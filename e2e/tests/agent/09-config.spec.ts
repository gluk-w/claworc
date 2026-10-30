import { test, expect } from "../../utils/base";
import { apiClient } from "../../utils/api";
import type { AgentType } from "../../utils/e2eConfig";
import { nonce, waitForRunning } from "../../utils/agentUi";

function safeJson(s: string): any {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}

// One harmless, type-appropriate edit per agent. Returns the new config text
// and a predicate that recognizes it in the config read back from the agent.
function editConfig(type: AgentType, current: string, marker: string): { next: string; applied: (s: string) => boolean } {
  switch (type) {
    case "openclaw": {
      // OpenClaw validates its config against a strict schema, so change a
      // known cosmetic key rather than adding one: the browser profile color.
      const color = `#${Buffer.from(marker).toString("hex").slice(-6)}`;
      const cfg = JSON.parse(current);
      cfg.browser = { ...(cfg.browser ?? {}), color };
      return {
        next: JSON.stringify(cfg, null, 2),
        applied: (s) => safeJson(s)?.browser?.color === color,
      };
    }
    case "nanoclaw": {
      const cfg = JSON.parse(current);
      const value = (cfg.maxMessagesPerPrompt ?? 10) === 10 ? 11 : 10;
      cfg.maxMessagesPerPrompt = value;
      cfg.assistantName = cfg.assistantName ?? "NanoClaw";
      return {
        next: JSON.stringify(cfg, null, 2),
        applied: (s) => safeJson(s)?.maxMessagesPerPrompt === value,
      };
    }
    case "hermes": {
      // YAML: a trailing comment is a no-op for Hermes but survives the
      // raw config-set write.
      const line = `# claworc-e2e ${marker}`;
      return {
        next: `${current.replace(/\s*$/, "")}\n${line}\n`,
        applied: (s) => s.includes(line),
      };
    }
  }
}

test.describe("config", { tag: "@e2e" }, () => {
  // Saving restarts the agent service, so this spec runs last in each project.
  test("edit config in the Config tab, save, and read it back", async ({ page, shared, agentType }) => {
    test.setTimeout(5 * 60_000);
    const api = await apiClient();

    const before = (await (await api.get(`/api/v1/instances/${shared.id}/config`)).json()) as {
      config: string;
      language: string;
    };
    const { next, applied } = editConfig(agentType, before.config, nonce("cfg"));
    expect(applied(before.config)).toBe(false);

    await page.goto(`/instances/${shared.id}#config`);
    const editor = page.locator(".monaco-editor").first();
    await expect(editor).toBeVisible({ timeout: 30_000 });
    // Wait until Monaco shows the agent's current config, not a placeholder.
    await expect(editor.locator(".view-lines")).not.toBeEmpty({ timeout: 30_000 });

    // Replace the whole document through the editor model (@monaco-editor/react
    // exposes the loaded Monaco on window). Keyboard input is unreliable here:
    // auto-indent/auto-close corrupt typed JSON and Cmd+A is not delivered to
    // Monaco in headless Chrome on macOS. setValue fires the same change event
    // as typing, so the page's edit state updates exactly as for a user edit.
    await page.waitForFunction(() => (window as any).monaco?.editor?.getModels().length > 0);
    await page.evaluate((text) => {
      const monaco = (window as any).monaco;
      const editor = monaco.editor.getEditors()[0];
      editor.getModel().setValue(text);
    }, next);

    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("button", { name: "Saving..." })).toHaveCount(0, { timeout: 120_000 });

    // The agent's own config (read through the shim's config-get) has the edit…
    await expect
      .poll(
        async () => {
          const res = await api.get(`/api/v1/instances/${shared.id}/config`);
          return res.ok() ? applied(((await res.json()) as { config: string }).config) : false;
        },
        { timeout: 60_000 },
      )
      .toBe(true);

    // …and so does the editor after a reload. The save restarts the agent,
    // so the page's config fetch can land mid-restart; reload until it loads.
    await expect(async () => {
      await page.reload();
      await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15_000 });
      await page.waitForFunction(() => (window as any).monaco?.editor?.getEditors().length > 0);
      const shown = await page.evaluate(() => (window as any).monaco.editor.getEditors()[0].getModel().getValue());
      expect(applied(shown)).toBe(true);
    }).toPass({ timeout: 90_000, intervals: [2_000, 5_000] });

    // The agent comes back after the restart the save triggers.
    await waitForRunning(api, shared.id);
  });
});
