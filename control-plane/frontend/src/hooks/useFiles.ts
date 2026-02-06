import { useQuery } from "@tanstack/react-query";
import { browseFiles, readFile } from "@/api/files";

export function useBrowseFiles(instanceId: number, path: string, enabled = true) {
  return useQuery({
    queryKey: ["instances", instanceId, "files", "browse", path],
    queryFn: () => {
      console.log(`[useBrowseFiles] Fetching files for path: ${path}`);
      return browseFiles(instanceId, path);
    },
    enabled,
    staleTime: 0, // Always consider data stale
    refetchOnMount: true, // Always refetch on mount
  });
}

export function useReadFile(instanceId: number, path: string, enabled = true) {
  return useQuery({
    queryKey: ["instances", instanceId, "files", "read", path],
    queryFn: () => readFile(instanceId, path),
    enabled,
  });
}
