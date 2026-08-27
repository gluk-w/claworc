import client from "./client";
import type {
  ComposioKeyTestResult,
  Settings,
  SettingsUpdatePayload,
} from "@common/types/settings";

export async function fetchSettings(): Promise<Settings> {
  const { data } = await client.get<Settings>("/settings");
  return data;
}

export async function updateSettings(
  payload: SettingsUpdatePayload,
): Promise<Settings> {
  const { data } = await client.put<Settings>("/settings", payload);
  return data;
}

/**
 * Probes the Composio API for every permission Claworc needs. Pass the
 * plaintext key to test an unsaved value; omit it to test the stored one.
 */
export async function testComposioKey(payload: {
  api_key?: string;
}): Promise<ComposioKeyTestResult> {
  const { data } = await client.post<ComposioKeyTestResult>(
    "/settings/composio/test",
    payload,
  );
  return data;
}
