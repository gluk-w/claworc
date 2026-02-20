import { describe, it, expect } from "vitest";
import { getHealthStatus, HEALTH_COLORS, HEALTH_LABELS } from "./providerHealth";
import type { ProviderStats } from "@/types/settings";

function makeStats(overrides: Partial<ProviderStats> = {}): ProviderStats {
  return {
    provider: "openai",
    total_requests: 100,
    error_count: 0,
    error_rate: 0,
    avg_latency: 200,
    ...overrides,
  };
}

describe("getHealthStatus", () => {
  it("returns 'unknown' when stats is undefined", () => {
    expect(getHealthStatus(undefined)).toBe("unknown");
  });

  it("returns 'unknown' when total_requests is 0", () => {
    expect(getHealthStatus(makeStats({ total_requests: 0 }))).toBe("unknown");
  });

  it("returns 'healthy' when error rate is 0", () => {
    expect(getHealthStatus(makeStats({ error_rate: 0 }))).toBe("healthy");
  });

  it("returns 'healthy' when error rate is exactly 0.1", () => {
    expect(getHealthStatus(makeStats({ error_rate: 0.1 }))).toBe("healthy");
  });

  it("returns 'warning' when error rate is 0.11", () => {
    expect(getHealthStatus(makeStats({ error_rate: 0.11 }))).toBe("warning");
  });

  it("returns 'warning' when error rate is 0.5", () => {
    expect(getHealthStatus(makeStats({ error_rate: 0.5 }))).toBe("warning");
  });

  it("returns 'error' when error rate is above 0.5", () => {
    expect(getHealthStatus(makeStats({ error_rate: 0.51 }))).toBe("error");
  });

  it("returns 'error' when error rate is 1.0", () => {
    expect(getHealthStatus(makeStats({ error_rate: 1.0 }))).toBe("error");
  });
});

describe("HEALTH_COLORS", () => {
  it("has colors for all statuses", () => {
    expect(HEALTH_COLORS.healthy).toBeDefined();
    expect(HEALTH_COLORS.warning).toBeDefined();
    expect(HEALTH_COLORS.error).toBeDefined();
    expect(HEALTH_COLORS.unknown).toBeDefined();
  });
});

describe("HEALTH_LABELS", () => {
  it("has labels for all statuses", () => {
    expect(HEALTH_LABELS.healthy).toBe("Healthy");
    expect(HEALTH_LABELS.warning).toBe("Warnings");
    expect(HEALTH_LABELS.error).toBe("Errors");
    expect(HEALTH_LABELS.unknown).toBe("No data");
  });
});
