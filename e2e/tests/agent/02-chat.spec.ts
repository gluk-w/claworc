import { test, expect } from "../../utils/base";
import { chatTurn, nonce, openChat } from "../../utils/agentUi";

test.describe("chat", { tag: "@e2e" }, () => {
  test("multi-turn conversation keeps context across turns", async ({ page, shared }) => {
    test.skip(!shared.llm_model, "no LLM provider wired onto the shared instance");
    test.setTimeout(8 * 60_000);

    const input = await openChat(page, shared.id);
    // Start from a clean agent session so earlier runs can't leak context.
    const newChat = page.locator('button[title="New chat"]');
    if (await newChat.isVisible()) {
      await newChat.click();
      await expect(input).toBeEnabled({ timeout: 60_000 });
    }

    const word = nonce("kiwi");

    const first = await chatTurn(
      page,
      input,
      `Remember this code word for later: ${word}. Do not use any tools. Reply with just the word OK.`,
    );
    expect(first.length).toBeGreaterThan(0);

    // The second turn only succeeds if the agent kept the first turn's
    // context — the code word is never repeated in this prompt.
    const second = await chatTurn(
      page,
      input,
      "What was the code word I asked you to remember? Do not use any tools. Reply with only the code word.",
    );
    expect(second).toContain(word);

    // A third turn builds on both previous ones.
    const third = await chatTurn(
      page,
      input,
      "Now write that same code word in UPPERCASE letters. Do not use any tools. Reply with only the result.",
    );
    expect(third).toContain(word.toUpperCase());

    // Every turn is rendered in order: 3 user messages, ≥3 assistant replies.
    await expect(page.locator(".bg-blue-600.whitespace-pre-wrap")).toHaveCount(3);
  });
});
