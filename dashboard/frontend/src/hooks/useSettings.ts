import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { fetchSettings, updateSettings } from "@/api/settings";
import type { SettingsUpdate } from "@/types/settings";

export function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: fetchSettings,
  });
}

export function useUpdateSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: SettingsUpdate) => updateSettings(payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["settings"] }),
  });
}
