import { test } from "../../utils/base";
import { SHARED_ENV_VARS } from "../../utils/sharedEnv";
import { nonce, openTerminal, runInTerminal } from "../../utils/agentUi";

test.describe("terminal", { tag: "@e2e" }, () => {
  test("connects, runs commands and sees their output", async ({ page, shared }) => {
    test.setTimeout(2 * 60_000);
    const xterm = await openTerminal(page, shared.id);

    const marker = nonce("term");
    // Arithmetic proves the shell evaluated the command, not just echoed input.
    await runInTerminal(page, xterm, `echo "${marker}-$((6*7))"`, `${marker}-42`);
    await runInTerminal(page, xterm, `echo "<<$(whoami)>>"`, "<<claworc>>");
  });

  test("env vars set at create time are visible", async ({ page, shared }) => {
    test.setTimeout(2 * 60_000);
    const xterm = await openTerminal(page, shared.id);

    // Wrap each value in a marker so the echoed command line can't match.
    for (const [name, value] of Object.entries(SHARED_ENV_VARS)) {
      const marker = `<<E2E:${name}>>`;
      await runInTerminal(page, xterm, `echo "${marker}$${name}${marker}"`, `${marker}${value}${marker}`);
    }
  });
});
