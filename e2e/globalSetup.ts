import { request } from "@playwright/test";
import { writeSharedInstance, clearSharedInstance } from "./utils/sharedInstance";
import { SHARED_ENV_VARS } from "./utils/sharedEnv";

const baseURL = process.env.BASE_URL ?? "http://localhost:18001";

export default async function globalSetup() {
  clearSharedInstance();

  const api = await request.newContext({ baseURL });
  const display_name = `e2e-shared-${Date.now()}`;

  // eslint-disable-next-line no-console
  console.log(`[globalSetup] creating shared instance ${display_name}`);
  const created = await api.post("/api/v1/instances", {
    headers: { "Content-Type": "application/json" },
    data: { display_name, env_vars_set: SHARED_ENV_VARS },
  });
  if (!created.ok()) {
    throw new Error(`globalSetup: instance create failed: ${created.status()} ${await created.text()}`);
  }
  const body = (await created.json()) as { id: number };

  // Poll until status=running (the instance is ready for terminal/files/etc.).
  const deadline = Date.now() + 4 * 60_000;
  while (Date.now() < deadline) {
    const res = await api.get(`/api/v1/instances/${body.id}`);
    if (res.ok()) {
      const inst = (await res.json()) as { status: string };
      if (inst.status === "running") {
        writeSharedInstance({ id: body.id, display_name });
        // eslint-disable-next-line no-console
        console.log(`[globalSetup] shared instance ${body.id} is running`);
        await api.dispose();
        return;
      }
    }
    await new Promise((r) => setTimeout(r, 2000));
  }
  await api.dispose();
  throw new Error(`globalSetup: shared instance ${body.id} never reached status=running`);
}
