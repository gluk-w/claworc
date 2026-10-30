import { request } from "@playwright/test";
import { readAllSharedInstances, clearSharedInstance } from "./utils/sharedInstance";

const baseURL = process.env.BASE_URL ?? "http://localhost:18001";

export default async function globalTeardown() {
  if (process.env.E2E_KEEP_INSTANCES) return;
  const shared = readAllSharedInstances();
  if (shared.length === 0) return;
  const api = await request.newContext({ baseURL });
  try {
    for (const inst of shared) {
      try {
        await api.delete(`/api/v1/instances/${inst.id}`);
        // eslint-disable-next-line no-console
        console.log(`[globalTeardown] deleted ${inst.agent_type} instance ${inst.id}`);
      } catch {
        // best-effort; run.sh tears down the namespace anyway.
      }
    }
  } finally {
    await api.dispose();
    clearSharedInstance();
  }
}
