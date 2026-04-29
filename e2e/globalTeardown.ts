import { request } from "@playwright/test";
import { readSharedInstance, clearSharedInstance } from "./utils/sharedInstance";

const baseURL = process.env.BASE_URL ?? "http://localhost:18001";

export default async function globalTeardown() {
  const shared = readSharedInstance();
  if (!shared) return;
  const api = await request.newContext({ baseURL });
  try {
    await api.delete(`/api/v1/instances/${shared.id}`);
    // eslint-disable-next-line no-console
    console.log(`[globalTeardown] deleted shared instance ${shared.id}`);
  } catch {
    // best-effort; run.sh tears down the namespace anyway.
  } finally {
    await api.dispose();
    clearSharedInstance();
  }
}
