import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
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
      toast.success("Settings saved");
    },
    onError: (error: any) => {
      toast.error(error.response?.data?.detail || "Failed to save settings");
    },
  });
}
