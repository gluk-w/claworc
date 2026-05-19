import client from "./client";

export interface UserListTeamRef {
  id: number;
  name: string;
  role: "user" | "manager";
}

export interface UserListInstanceRef {
  id: number;
  name: string;
  display_name: string;
  team_id: number;
  team_name: string;
}

export interface UserListItem {
  id: number;
  username: string;
  role: string;
  last_login_at: string;
  created_at: string;
  teams: UserListTeamRef[];
  instances: UserListInstanceRef[];
}

export async function fetchUsers(): Promise<UserListItem[]> {
  const res = await client.get("/users");
  return res.data;
}

export interface CreatedUser {
  id: number;
  username: string;
  role: string;
}

export async function createUser(data: {
  username: string;
  password: string;
  role: string;
}): Promise<CreatedUser> {
  const res = await client.post("/users", data);
  return res.data;
}

export async function deleteUser(id: number): Promise<void> {
  await client.delete(`/users/${id}`);
}

export async function updateUserRole(
  id: number,
  role: string,
): Promise<void> {
  await client.put(`/users/${id}/role`, { role });
}

export async function getUserInstances(
  id: number,
): Promise<{ instance_ids: number[] }> {
  const res = await client.get(`/users/${id}/instances`);
  return res.data;
}

export interface UserTeamMembershipDTO {
  team_id: number;
  name: string;
  role: "user" | "manager";
}

export async function getUserTeams(
  id: number,
): Promise<UserTeamMembershipDTO[]> {
  const res = await client.get(`/users/${id}/teams`);
  return res.data;
}

export async function setUserInstances(
  id: number,
  instanceIds: number[],
): Promise<void> {
  await client.put(`/users/${id}/instances`, { instance_ids: instanceIds });
}

export async function resetUserPassword(
  id: number,
  password: string,
): Promise<void> {
  await client.post(`/users/${id}/reset-password`, { password });
}
