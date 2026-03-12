import client from "./client";
import type { ListDirectoryResponse, ReadFileResponse, FileEntry } from "@/types/files";

export interface SearchResponse {
  path: string;
  query: string;
  results: FileEntry[];
}

export async function browseFiles(
  instanceId: number,
  path: string,
): Promise<ListDirectoryResponse> {
  const { data } = await client.get<ListDirectoryResponse>(
    `/instances/${instanceId}/files/browse`,
    { params: { path } },
  );
  return data;
}

export async function readFile(
  instanceId: number,
  path: string,
): Promise<ReadFileResponse> {
  const { data } = await client.get<ReadFileResponse>(
    `/instances/${instanceId}/files/read`,
    { params: { path } },
  );
  return data;
}

export function downloadFile(instanceId: number, path: string): void {
  const url = `/api/v1/instances/${instanceId}/files/download?path=${encodeURIComponent(path)}`;
  window.open(url, "_blank");
}

export async function createFile(
  instanceId: number,
  path: string,
  content: string = "",
): Promise<{ success: boolean; path: string }> {
  const { data } = await client.post(
    `/instances/${instanceId}/files/create`,
    { path, content },
  );
  return data;
}

export async function createDirectory(
  instanceId: number,
  path: string,
): Promise<{ success: boolean; path: string }> {
  const { data } = await client.post(
    `/instances/${instanceId}/files/mkdir`,
    { path },
  );
  return data;
}

export async function uploadFile(
  instanceId: number,
  path: string,
  file: File,
): Promise<{ success: boolean; path: string; filename: string }> {
  const formData = new FormData();
  formData.append("file", file);

  const { data } = await client.post(
    `/instances/${instanceId}/files/upload?path=${encodeURIComponent(path)}`,
    formData,
    {
      headers: {
        "Content-Type": "multipart/form-data",
      },
    },
  );
  return data;
}

export async function deleteFile(
  instanceId: number,
  path: string,
): Promise<{ success: boolean }> {
  const { data } = await client.delete(`/instances/${instanceId}/files`, {
    params: { path },
  });
  return data;
}

export async function renameFile(
  instanceId: number,
  from: string,
  to: string,
): Promise<{ success: boolean; path: string }> {
  const { data } = await client.post(`/instances/${instanceId}/files/rename`, {
    from,
    to,
  });
  return data;
}

export async function searchFiles(
  instanceId: number,
  path: string,
  query: string,
): Promise<SearchResponse> {
  const { data } = await client.get<SearchResponse>(
    `/instances/${instanceId}/files/search`,
    { params: { path, query } },
  );
  return data;
}
