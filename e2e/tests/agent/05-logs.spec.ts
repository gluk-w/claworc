import { test, expect } from "../../utils/base";
import { AGENT_LOG_PATH } from "../../utils/e2eConfig";
import { nonce, openTerminal, runInTerminal } from "../../utils/agentUi";

test.describe("logs", { tag: "@e2e" }, () => {
  test("logs tab streams new agent log lines live", async ({ page, context, shared }) => {
    test.setTimeout(2 * 60_000);

    const logsPage = page;
    await logsPage.goto(`/instances/${shared.id}#logs`);
    await expect(logsPage.getByText("Connected", { exact: true })).toBeVisible({ timeout: 30_000 });

    // Append a unique line to the agent log from a second tab's terminal; the
    // already-open Logs tab must receive it over the SSE stream without a
    // reload.
    const marker = nonce("logline");
    const termPage = await context.newPage();
    const xterm = await openTerminal(termPage, shared.id);
    await runInTerminal(
      termPage,
      xterm,
      `echo "e2e ${marker}" >> ${AGENT_LOG_PATH} && echo "<<app""ended>>"`,
      "<<appended>>",
    );
    await termPage.close();

    await expect(logsPage.getByText(`e2e ${marker}`)).toBeVisible({ timeout: 30_000 });
    await expect(logsPage.getByText("Connected", { exact: true })).toBeVisible();
  });
});
