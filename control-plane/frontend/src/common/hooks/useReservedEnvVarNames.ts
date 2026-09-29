import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchReservedEnvVarNames } from "@common/api/reservedEnvVars";

// Used until the API answers (or if it fails). The authoritative list comes
// from GET /api/v1/env-vars/reserved (ReservedEnvVarNames in
// control-plane/internal/handlers/envvars.go), which also includes
// agent-specific legacy names.
const FALLBACK_RESERVED = [
  "CLAWORC_INSTANCE_ID",
  "CLAWORC_CONNECTION_SECRET",
  "CLAWORC_AGENT_TOKEN",
  "CLAWORC_INITIAL_LLM_CONFIG",
  "CLAWORC_LLM_PROXY_URL",
];

/** Set of env var names reserved by the control plane. */
export function useReservedEnvVarNames(): Set<string> {
  const { data } = useQuery({
    queryKey: ["env-vars-reserved"],
    queryFn: fetchReservedEnvVarNames,
    staleTime: 30 * 60 * 1000,
  });
  return useMemo(() => new Set([...FALLBACK_RESERVED, ...(data ?? [])]), [data]);
}
