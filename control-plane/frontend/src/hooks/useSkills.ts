import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  deleteSkill,
  deploySkill,
  listSkills,
  searchClawhub,
  uploadSkill,
} from "@/api/skills";
import { errorToast, successToast } from "@/utils/toast";

export function useSkills() {
  return useQuery({
    queryKey: ["skills"],
    queryFn: listSkills,
  });
}

export function useUploadSkill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (file: File) => uploadSkill(file),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      successToast("Skill uploaded");
    },
    onError: (error) => errorToast("Failed to upload skill", error),
  });
}

export function useDeleteSkill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (slug: string) => deleteSkill(slug),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      successToast("Skill deleted");
    },
    onError: (error) => errorToast("Failed to delete skill", error),
  });
}

export function useClawhubSearch(q: string, enabled: boolean) {
  return useQuery({
    queryKey: ["clawhub-search", q],
    queryFn: () => searchClawhub(q),
    enabled: enabled && q.trim().length > 0,
    staleTime: 60_000,
  });
}

export function useDeploySkill() {
  return useMutation({
    mutationFn: ({
      slug,
      instanceIds,
      source,
      version,
    }: {
      slug: string;
      instanceIds: number[];
      source: "library" | "clawhub";
      version?: string;
    }) => deploySkill(slug, instanceIds, source, version),
  });
}
