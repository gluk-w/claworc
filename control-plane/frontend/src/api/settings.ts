import client from "./client";
import type {
  Settings,
  SettingsUpdatePayload,
  ProviderAnalyticsResponse,
  TestProviderKeyRequest,
  TestProviderKeyResponse,
} from "@/types/settings";

export type { TestProviderKeyRequest, TestProviderKeyResponse };

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

export async function testProviderKey(
  payload: TestProviderKeyRequest,
): Promise<TestProviderKeyResponse> {
  const { data } = await client.post<TestProviderKeyResponse>(
    "/settings/test-provider-key",
    payload,
  );
  return data;
}

export async function fetchProviderAnalytics(): Promise<ProviderAnalyticsResponse> {
  const { data } = await client.get<ProviderAnalyticsResponse>(
    "/analytics/providers",
  );
  return data;
}
