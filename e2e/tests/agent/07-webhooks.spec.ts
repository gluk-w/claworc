import { test, expect } from "../../utils/base";
import { apiClient } from "../../utils/api";
import { nonce } from "../../utils/agentUi";

interface WebhookInfo {
  instance_uuid: string;
  public_url: string;
  keys: Array<{ id: number; key: string; label: string; is_private: boolean }>;
}

test.describe("webhooks", { tag: "@e2e" }, () => {
  test("public webhook key: create in UI, call it, conversation spans requests", async ({
    page,
    shared,
  }) => {
    test.skip(!shared.llm_model, "no LLM provider wired onto the shared instance");
    test.setTimeout(6 * 60_000);
    const api = await apiClient();
    const label = nonce("e2ehook");

    // Create a public key from the agent's Settings tab.
    await page.goto(`/instances/${shared.id}#settings`);
    const section = page.locator("div.rounded-lg.border", {
      has: page.getByRole("heading", { name: "Webhook", exact: true }),
    });
    await expect(section).toBeVisible({ timeout: 30_000 });
    await section.getByRole("button", { name: "Add key" }).click();
    await page.getByPlaceholder("e.g. zapier, partner-x").fill(label);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(section.getByTitle(label)).toBeVisible({ timeout: 15_000 });

    const info = (await (await api.get(`/api/v1/instances/${shared.id}/webhook`)).json()) as WebhookInfo;
    const key = info.keys.find((k) => k.label === label);
    expect(key, "created key missing from webhook API").toBeTruthy();
    expect(key!.is_private).toBe(false);

    try {
      const url = `/webhooks/${info.instance_uuid}`;
      const session_name = nonce("e2e-session");
      const word = nonce("mango");
      const call = (message: string, token = key!.key) =>
        api.post(url, {
          headers: { Authorization: `Bearer ${token}` },
          data: { session_name, message },
          timeout: 180_000,
        });

      // A wrong token is rejected.
      const denied = await call("hello", "0".repeat(128));
      expect(denied.status()).toBe(401);

      const first = await call(
        `Remember this code word for later: ${word}. Do not use any tools. Reply with just the word OK.`,
      );
      expect(first.status(), await first.text()).toBe(200);
      expect(first.headers()["content-type"]).toContain("text/plain");
      expect((await first.text()).trim().length).toBeGreaterThan(0);

      // Same session_name → same agent session: the second request can only
      // answer from the first request's context.
      const second = await call(
        "What was the code word I asked you to remember? Do not use any tools. Reply with only the code word.",
      );
      expect(second.status(), await second.text()).toBe(200);
      expect(await second.text()).toContain(word);

      // Both calls show up in the webhook event log.
      await expect
        .poll(async () => {
          const res = await api.get(`/api/v1/instances/${shared.id}/webhook/logs`);
          const body = await res.json();
          const logs: Array<{ status_code: number; key_last4: string }> = Array.isArray(body)
            ? body
            : body.logs ?? [];
          return logs.filter((l) => l.status_code === 200 && l.key_last4 === key!.key.slice(-4)).length;
        })
        .toBeGreaterThanOrEqual(2);
    } finally {
      await api.delete(`/api/v1/instances/${shared.id}/webhook/keys/${key!.id}`);
    }
  });
});
