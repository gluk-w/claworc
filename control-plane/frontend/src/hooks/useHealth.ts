import { useQuery } from "@tanstack/react-query";
import { fetchHealth } from "@/api/health";

export function useHealth() {
  return useQuery({
    queryKey: ["health"],
    queryFn: fetchHealth,
    refetchInterval: 10_000,
  });
}
