import client from "./client";
import type { InstanceSlack, InstanceSlackUpdatePayload } from "@common/types/slack";

export async function fetchInstanceSlack(instanceId: number): Promise<InstanceSlack> {
  const { data } = await client.get<InstanceSlack>(`/instances/${instanceId}/slack`);
  return data;
}

export async function updateInstanceSlack(
  instanceId: number,
  payload: InstanceSlackUpdatePayload,
): Promise<InstanceSlack> {
  const { data } = await client.put<InstanceSlack>(`/instances/${instanceId}/slack`, payload);
  return data;
}
