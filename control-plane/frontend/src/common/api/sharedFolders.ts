import client from "./client";

export interface SharedFolder {
  id: number;
  name: string;
  mount_path: string;
  host_path: string;
  read_only: boolean;
  owner_id: number;
  instance_ids: number[];
  team_ids: number[];
  created_at: string;
}

export interface HostMountConfig {
  enabled: boolean;
  allowed_prefixes: string[];
}

export async function fetchSharedFolders(): Promise<SharedFolder[]> {
  const res = await client.get("/shared-folders");
  return res.data;
}

export async function fetchHostMountConfig(): Promise<HostMountConfig> {
  const res = await client.get("/shared-folders/host-mount-config");
  return res.data;
}

export async function createSharedFolder(data: {
  name: string;
  mount_path: string;
  host_path?: string;
  read_only?: boolean;
}): Promise<SharedFolder> {
  const res = await client.post("/shared-folders", data);
  return res.data;
}

export async function getSharedFolder(id: number): Promise<SharedFolder> {
  const res = await client.get(`/shared-folders/${id}`);
  return res.data;
}

export async function updateSharedFolder(
  id: number,
  data: {
    name?: string;
    mount_path?: string;
    read_only?: boolean;
    instance_ids?: number[];
    team_ids?: number[];
  },
): Promise<void> {
  await client.put(`/shared-folders/${id}`, data);
}

export async function deleteSharedFolder(id: number): Promise<void> {
  await client.delete(`/shared-folders/${id}`);
}
