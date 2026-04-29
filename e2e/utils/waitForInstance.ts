import { APIRequestContext, expect } from "@playwright/test";
import { getInstance } from "./api";

export async function waitForInstanceStatus(
  api: APIRequestContext,
  id: number | string,
  expected: string,
  timeoutMs = 180_000,
): Promise<Record<string, unknown>> {
  const deadline = Date.now() + timeoutMs;
  let last: Record<string, unknown> | null = null;
  while (Date.now() < deadline) {
    last = await getInstance(api, id);
    if (last && (last as { status?: string }).status === expected) {
      return last;
    }
    await new Promise((r) => setTimeout(r, 2000));
  }
  expect.soft(last, `instance ${id} never reached status=${expected}`).toMatchObject({
    status: expected,
  });
  throw new Error(`timeout waiting for instance ${id} to reach status=${expected}`);
}
