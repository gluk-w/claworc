import client from "./client";

/** Env var names the control plane sets itself; users may not define them. */
export async function fetchReservedEnvVarNames(): Promise<string[]> {
  const { data } = await client.get<{ names: string[] }>("/env-vars/reserved");
  return data.names ?? [];
}
