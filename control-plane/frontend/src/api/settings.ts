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
