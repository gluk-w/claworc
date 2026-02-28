import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { successToast, errorToast } from "@/utils/toast";
import { fetchSettings, updateSettings } from "@/api/settings";
import type { SettingsUpdatePayload } from "@/types/settings";

export function useSettings() {
  return useQuery({
    queryKey: ["settings"],
    queryFn: fetchSettings,
  });
}

export function useUpdateSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: SettingsUpdatePayload) => updateSettings(payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["settings"] });
      successToast("Settings saved");
    },
    onError: (error: any) => {
      errorToast("Failed to save settings", error);
    },
  });
}
