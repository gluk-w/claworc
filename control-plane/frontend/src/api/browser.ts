import client from "./client";

// Status of the on-demand browser pod for a non-legacy instance.
//   "stopped"  — pod is not running; first CDP/VNC use will spawn it
//   "starting" — spawn task is in flight
//   "running"  — pod is up and reachable
//   "error"    — last spawn attempt failed; ErrorMsg has details
//   "legacy"   — instance still uses the combined image; show migration banner
//   "disabled" — server has no browser bridge configured (dev/test only)
export type BrowserState =
  | "stopped"
  | "starting"
  | "running"
  | "error"
  | "legacy"
  | "disabled";

export interface BrowserStatus {
  state: BrowserState;
  is_legacy_embedded: boolean;
  started_at?: string;
  last_used_at?: string;
  error_msg?: string;
}

export async function fetchBrowserStatus(
  instanceID: number,
): Promise<BrowserStatus> {
  const { data } = await client.get<BrowserStatus>(
    `/instances/${instanceID}/browser/status`,
  );
  return data;
}

export async function startBrowser(
  instanceID: number,
): Promise<BrowserStatus> {
  const { data } = await client.post<BrowserStatus>(
    `/instances/${instanceID}/browser/start`,
  );
  return data;
}

export async function stopBrowser(
  instanceID: number,
): Promise<{ state: BrowserState }> {
  const { data } = await client.post<{ state: BrowserState }>(
    `/instances/${instanceID}/browser/stop`,
  );
  return data;
}

export async function migrateBrowser(
  instanceID: number,
): Promise<{ task_id: string }> {
  const { data } = await client.post<{ task_id: string }>(
    `/instances/${instanceID}/browser/migrate`,
  );
  return data;
}
