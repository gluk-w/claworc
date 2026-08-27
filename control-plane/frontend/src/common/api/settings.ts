import client from "./client";
import type { Settings, SettingsUpdatePayload } from "@common/types/settings";

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

export interface ChannelAlertTestResult {
  status: "sent" | "failed";
  http_status?: number;
  error?: string;
}

export async function testChannelAlertWebhook(): Promise<ChannelAlertTestResult> {
  const { data } = await client.post<ChannelAlertTestResult>(
    "/settings/channel-alerts/test",
  );
  return data;
}
