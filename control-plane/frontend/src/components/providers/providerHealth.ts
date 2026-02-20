import type { ProviderStats, ProviderHealthStatus } from "@/types/settings";

/**
 * Derives a health status from provider analytics stats.
 * - "healthy": error rate <= 10%
 * - "warning": error rate > 10% but <= 50%
 * - "error": error rate > 50%
 * - "unknown": no data available
 */
export function getHealthStatus(
  stats: ProviderStats | undefined,
): ProviderHealthStatus {
  if (!stats || stats.total_requests === 0) return "unknown";
  if (stats.error_rate > 0.5) return "error";
  if (stats.error_rate > 0.1) return "warning";
  return "healthy";
}

export const HEALTH_COLORS: Record<ProviderHealthStatus, string> = {
  healthy: "#22C55E",
  warning: "#EAB308",
  error: "#EF4444",
  unknown: "#9CA3AF",
};

export const HEALTH_LABELS: Record<ProviderHealthStatus, string> = {
  healthy: "Healthy",
  warning: "Warnings",
  error: "Errors",
  unknown: "No data",
};
