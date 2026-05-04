// Validation helpers for Kubernetes-style resource quantity strings used by
// instance and global-default settings. Mirrors the backend rules in
// control-plane/internal/handlers/resourcevalidation.go.

const CPU_RE = /^(\d+m|\d+(\.\d+)?)$/;
const MEM_RE = /^\d+(Ki|Mi|Gi)$/;

export const isValidCPU = (v: string): boolean => CPU_RE.test(v);
export const isValidMemory = (v: string): boolean => MEM_RE.test(v);
export const isValidStorage = isValidMemory;
export const isValidResolution = (v: string): boolean => v === "" || /^\d+x\d+$/.test(v);

export const cpuToMillis = (v: string): number => {
  if (v.endsWith("m")) return parseInt(v, 10);
  return parseFloat(v) * 1000;
};

export const memToBytes = (v: string): number => {
  const n = parseInt(v, 10);
  if (v.endsWith("Gi")) return n * 1024 * 1024 * 1024;
  if (v.endsWith("Mi")) return n * 1024 * 1024;
  if (v.endsWith("Ki")) return n * 1024;
  return n;
};

export type ResourceQuantities = {
  cpu_request?: string;
  cpu_limit?: string;
  memory_request?: string;
  memory_limit?: string;
  storage_home?: string;
  storage_homebrew?: string;
};

// Returns a per-field error map. Empty values are treated as "not provided"
// and skipped, matching the backend tolerance.
export function validateResourceQuantities(
  q: ResourceQuantities,
): Partial<Record<keyof ResourceQuantities | "cpu_pair" | "memory_pair", string>> {
  const errors: Partial<
    Record<keyof ResourceQuantities | "cpu_pair" | "memory_pair", string>
  > = {};

  if (q.cpu_request && !isValidCPU(q.cpu_request))
    errors.cpu_request = "e.g. 500m or 0.5";
  if (q.cpu_limit && !isValidCPU(q.cpu_limit))
    errors.cpu_limit = "e.g. 2000m or 2";
  if (q.memory_request && !isValidMemory(q.memory_request))
    errors.memory_request = "e.g. 1Gi or 512Mi";
  if (q.memory_limit && !isValidMemory(q.memory_limit))
    errors.memory_limit = "e.g. 4Gi or 2048Mi";
  if (q.storage_home && !isValidStorage(q.storage_home))
    errors.storage_home = "e.g. 10Gi";
  if (q.storage_homebrew && !isValidStorage(q.storage_homebrew))
    errors.storage_homebrew = "e.g. 10Gi";

  if (
    !errors.cpu_request &&
    !errors.cpu_limit &&
    q.cpu_request &&
    q.cpu_limit &&
    cpuToMillis(q.cpu_request) > cpuToMillis(q.cpu_limit)
  ) {
    errors.cpu_pair = "CPU request cannot exceed CPU limit";
  }
  if (
    !errors.memory_request &&
    !errors.memory_limit &&
    q.memory_request &&
    q.memory_limit &&
    memToBytes(q.memory_request) > memToBytes(q.memory_limit)
  ) {
    errors.memory_pair = "Memory request cannot exceed memory limit";
  }
  return errors;
}
