import client from "./client";

export interface Team {
  id: number;
  name: string;
  description: string;
  member_count?: number;
  instance_count?: number;
}

export interface TeamMember {
  user_id: number;
  username: string;
  role: "user" | "manager";
  user_role: "admin" | "user";
}

export async function fetchTeams(): Promise<Team[]> {
  const { data } = await client.get<Team[]>("/teams");
  return data;
}

export async function createTeam(payload: {
  name: string;
  description?: string;
}): Promise<Team> {
  const { data } = await client.post<Team>("/teams", payload);
  return data;
}

export async function updateTeam(
  id: number,
  payload: { name?: string; description?: string },
): Promise<Team> {
  const { data } = await client.put<Team>(`/teams/${id}`, payload);
  return data;
}

export async function deleteTeam(id: number): Promise<void> {
  await client.delete(`/teams/${id}`);
}

export async function fetchTeamMembers(id: number): Promise<TeamMember[]> {
  const { data } = await client.get<TeamMember[]>(`/teams/${id}/members`);
  return data;
}

export async function setTeamMember(
  id: number,
  payload: { user_id: number; role: "user" | "manager" },
): Promise<void> {
  await client.post(`/teams/${id}/members`, payload);
}

export async function removeTeamMember(
  id: number,
  userId: number,
): Promise<void> {
  await client.delete(`/teams/${id}/members/${userId}`);
}

export async function fetchTeamProviderIDs(id: number): Promise<number[]> {
  const { data } = await client.get<{ provider_ids: number[] }>(
    `/teams/${id}/providers`,
  );
  return data.provider_ids ?? [];
}

export async function setTeamProviderIDs(
  id: number,
  providerIds: number[],
): Promise<void> {
  await client.put(`/teams/${id}/providers`, { provider_ids: providerIds });
}
