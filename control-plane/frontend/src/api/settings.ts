import client from "./client";
import type { Settings, SettingsUpdatePayload } from "@/types/settings";

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

export interface TestProviderKeyRequest {
  provider: string;
  api_key: string;
  base_url?: string;
}

export interface TestProviderKeyResponse {
  success: boolean;
  message: string;
  details?: string;
}

export async function testProviderKey(
  payload: TestProviderKeyRequest,
): Promise<TestProviderKeyResponse> {
  const { data } = await client.post<TestProviderKeyResponse>(
    "/settings/test-provider-key",
    payload,
  );
  return data;
}
