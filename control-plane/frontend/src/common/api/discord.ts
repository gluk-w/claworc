import client from "./client";
import type { InstanceDiscord, InstanceDiscordUpdatePayload } from "@common/types/discord";

export async function fetchInstanceDiscord(instanceId: number): Promise<InstanceDiscord> {
  const { data } = await client.get<InstanceDiscord>(`/instances/${instanceId}/discord`);
  return data;
}

export async function updateInstanceDiscord(
  instanceId: number,
  payload: InstanceDiscordUpdatePayload,
): Promise<InstanceDiscord> {
  const { data } = await client.put<InstanceDiscord>(`/instances/${instanceId}/discord`, payload);
  return data;
}
