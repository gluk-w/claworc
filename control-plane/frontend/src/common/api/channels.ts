import client from "./client";
import type { ChannelHealth } from "@common/types/channel";

export async function getChannelHealth(instanceId: number): Promise<ChannelHealth> {
  const { data } = await client.get<ChannelHealth>(`/instances/${instanceId}/channels/health`);
  return data;
}
