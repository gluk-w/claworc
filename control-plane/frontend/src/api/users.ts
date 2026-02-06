import client from "./client";

export interface UserListItem {
  id: number;
  username: string;
  role: string;
  created_at: string;
}

export async function fetchUsers(): Promise<UserListItem[]> {
  const res = await client.get("/users");
  return res.data;
}

export async function createUser(data: {
  username: string;
  password: string;
  role: string;
}): Promise<void> {
  await client.post("/users", data);
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
