import { APIRequestContext, Locator, Page } from "@playwright/test";
import { expect } from "./base";

// Random, lowercase, prompt-safe token used to prove that specific content
// round-tripped (and to keep reruns from matching a previous run's output).
export function nonce(prefix = "e2e"): string {
  return `${prefix}${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
}

// ---- Terminal -------------------------------------------------------------

export async function openTerminal(page: Page, instanceId: number): Promise<Locator> {
  await page.goto(`/instances/${instanceId}#terminal`);
  const xterm = page.locator(".xterm").first();
  await expect(xterm).toBeVisible({ timeout: 30_000 });
  // Interactive shell prompt — bash PS1 ends in `$ ` or `# `.
  await expect(xterm).toContainText(/[$#]\s/, { timeout: 30_000 });
  await xterm.click();
  return xterm;
}

// Types a command and waits for `expected` in the terminal. Wrap the
// interesting output in a marker the command itself doesn't contain verbatim
// (e.g. `echo "<<M>>$VAR<<M>>"`), so the echoed command line can't satisfy the
// assertion on its own.
export async function runInTerminal(
  page: Page,
  xterm: Locator,
  command: string,
  expected: string | RegExp,
  timeout = 15_000,
): Promise<void> {
  await page.keyboard.type(command);
  await page.keyboard.press("Enter");
  await expect(xterm).toContainText(expected, { timeout });
}

// ---- Chat -----------------------------------------------------------------

// Assistant replies render as markdown bubbles; user messages don't.
export const assistantBubbles = (page: Page) => page.locator(".markdown-agent");

export async function openChat(page: Page, instanceId: number): Promise<Locator> {
  await page.goto(`/instances/${instanceId}#chat`);
  const input = page.getByPlaceholder("Type a message...");
  // The textarea is disabled (placeholder "Waiting for connection...") until
  // the chat WebSocket reports connected.
  await expect(input).toBeEnabled({ timeout: 90_000 });
  return input;
}

// Sends one chat message and waits for the turn to finish, returning the
// final text of the reply. The chat UI has no explicit end-of-turn marker:
// the thinking indicator and Stop button disappear as soon as the first
// streamed snapshot arrives, so we wait for a new assistant bubble, for the
// indicator/Stop to be gone, and then for the reply text to stop changing.
export async function chatTurn(page: Page, input: Locator, message: string, timeout = 150_000): Promise<string> {
  const bubbles = assistantBubbles(page);
  const before = await bubbles.count();
  // Scope to the chat composer: the page header has its own "Stop" (the
  // agent's stop button).
  const form = input.locator("xpath=ancestor::form[1]");
  await input.fill(message);
  await input.press("Enter");
  await expect(page.getByText(message, { exact: true }).last()).toBeVisible();
  await expect.poll(() => bubbles.count(), { timeout }).toBeGreaterThan(before);
  await expect(page.locator('[class*="animate-[pulse"]')).toHaveCount(0, { timeout });
  await expect(form.locator('button[title="Stop"]')).toHaveCount(0, { timeout });
  await expect(form.locator('button[title="Send"]')).toBeVisible();

  // Snapshots are cumulative; the reply is complete once the last bubble has
  // been stable for a few consecutive reads.
  let last = "";
  let stable = 0;
  const deadline = Date.now() + timeout;
  while (stable < 4 && Date.now() < deadline) {
    await page.waitForTimeout(500);
    const text = (await bubbles.last().innerText()).trim();
    stable = text === last && text !== "" ? stable + 1 : 0;
    last = text;
  }
  return last;
}

// ---- Instance API helpers ------------------------------------------------

export async function readInstanceFile(
  api: APIRequestContext,
  instanceId: number,
  path: string,
): Promise<string | null> {
  const res = await api.get(`/api/v1/instances/${instanceId}/files/read`, { params: { path } });
  if (!res.ok()) return null;
  return ((await res.json()) as { content: string }).content;
}

export async function waitForRunning(api: APIRequestContext, instanceId: number, timeoutMs = 180_000) {
  await expect
    .poll(
      async () => {
        const res = await api.get(`/api/v1/instances/${instanceId}`);
        return res.ok() ? ((await res.json()) as { status: string }).status : `http ${res.status()}`;
      },
      { timeout: timeoutMs, intervals: [2_000] },
    )
    .toBe("running");
}
