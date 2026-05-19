export type FileType = "file" | "directory" | "symlink";

export interface FileEntry {
  name: string;
  type: FileType;
  size: string | null;
  permissions: string;
}

export interface ListDirectoryResponse {
  path: string;
  entries: FileEntry[];
}

export interface ReadFileResponse {
  path: string;
  content: string;
}
