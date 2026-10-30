import { useQuery } from "@tanstack/react-query";
import { fetchAgentTypes } from "@common/api/agentTypes";

/** Static agent-type registry (with resolved default images). Rarely changes. */
export function useAgentTypes() {
  return useQuery({
    queryKey: ["agent-types"],
    queryFn: fetchAgentTypes,
    staleTime: 5 * 60 * 1000,
  });
}
