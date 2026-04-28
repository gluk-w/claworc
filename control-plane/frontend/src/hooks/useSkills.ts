import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  deleteSkill,
  deploySkill,
  getSkillFile,
  listSkillFiles,
  listSkills,
  saveSkillFile,
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
    mutationFn: ({ file, overwrite = false }: { file: File; overwrite?: boolean }) =>
      uploadSkill(file, overwrite),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["skills"] });
      successToast("Skill uploaded");
    },
    onError: (error, _vars, _ctx) => {
      // 409 conflicts are handled inline in the modal — suppress the toast
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      if ((error as any)?.response?.status === 409) return;
      errorToast("Failed to upload skill", error);
    },
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

export function useSkillFiles(slug: string | null) {
  return useQuery({
    queryKey: ["skill-files", slug],
    queryFn: () => listSkillFiles(slug as string),
    enabled: !!slug,
  });
}

export function useSkillFile(slug: string | null, path: string | null) {
  return useQuery({
    queryKey: ["skill-file", slug, path],
    queryFn: () => getSkillFile(slug as string, path as string),
    enabled: !!slug && !!path,
  });
}

export function useSaveSkillFile(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ path, content }: { path: string; content: string }) =>
      saveSkillFile(slug, path, content),
    onSuccess: (_data, { path }) => {
      qc.invalidateQueries({ queryKey: ["skill-files", slug] });
      qc.invalidateQueries({ queryKey: ["skill-file", slug, path] });
      if (path === "SKILL.md") {
        qc.invalidateQueries({ queryKey: ["skills"] });
      }
      successToast("File saved");
    },
    onError: (error) => errorToast("Failed to save file", error),
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
