import client from "./client";
import type { SSHStatusResponse, SSHTestResponse, SSHEventsResponse, SSHReconnectResponse, SSHFingerprintResponse } from "@/types/ssh";

export async function fetchSSHStatus(instanceId: number): Promise<SSHStatusResponse> {
  const { data } = await client.get<SSHStatusResponse>(`/instances/${instanceId}/ssh-status`);
  return data;
}

export async function testSSHConnection(instanceId: number): Promise<SSHTestResponse> {
  const { data } = await client.get<SSHTestResponse>(`/instances/${instanceId}/ssh-test`);
  return data;
}

export async function fetchSSHEvents(instanceId: number): Promise<SSHEventsResponse> {
  const { data } = await client.get<SSHEventsResponse>(`/instances/${instanceId}/ssh-events`);
  return data;
}

export async function reconnectSSH(instanceId: number): Promise<SSHReconnectResponse> {
  const { data } = await client.post<SSHReconnectResponse>(`/instances/${instanceId}/ssh-reconnect`);
  return data;
}

export async function fetchSSHFingerprint(): Promise<SSHFingerprintResponse> {
  const { data } = await client.get<SSHFingerprintResponse>(`/ssh-fingerprint`);
  return data;
}
