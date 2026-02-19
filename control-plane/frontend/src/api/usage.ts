import client from "./client";
import type {
  UsageSummary,
  InstanceUsageSummary,
  InstanceLimits,
  BudgetLimit,
  RateLimit,
  ProxyStatus,
} from "@/types/usage";

export async function fetchUsage(params?: {
  since?: string;
  until?: string;
  group_by?: string;
}): Promise<UsageSummary[]> {
  const { data } = await client.get<UsageSummary[]>("/usage", { params });
  return data;
}

export async function fetchInstanceUsage(
  id: number,
  params?: { since?: string; until?: string },
): Promise<InstanceUsageSummary[]> {
  const { data } = await client.get<InstanceUsageSummary[]>(
    `/instances/${id}/usage`,
    { params },
  );
  return data;
}

export async function fetchInstanceLimits(
  id: number,
): Promise<InstanceLimits> {
  const { data } = await client.get<InstanceLimits>(
    `/instances/${id}/limits`,
  );
  return data;
}

export async function setInstanceBudget(
  id: number,
  budget: BudgetLimit,
): Promise<void> {
  await client.put(`/instances/${id}/budget`, budget);
}

export async function setInstanceRateLimit(
  id: number,
  rateLimits: RateLimit[],
): Promise<void> {
  await client.put(`/instances/${id}/ratelimit`, rateLimits);
}

export async function fetchProxyStatus(): Promise<ProxyStatus> {
  const { data } = await client.get<ProxyStatus>("/proxy/status");
  return data;
}
