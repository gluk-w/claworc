import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  useSSHStatus,
  useSSHEvents,
  useSSHTest,
  useSSHReconnect,
  useSSHFingerprint,
  useGlobalSSHStatus,
  useSSHMetrics,
} from "../useSSH";

vi.mock("@/api/ssh", () => ({
  fetchSSHStatus: vi.fn(),
  fetchSSHEvents: vi.fn(),
  testSSHConnection: vi.fn(),
  reconnectSSH: vi.fn(),
  fetchSSHFingerprint: vi.fn(),
  fetchGlobalSSHStatus: vi.fn(),
  fetchSSHMetrics: vi.fn(),
}));

import {
  fetchSSHStatus,
  fetchSSHEvents,
  testSSHConnection,
  reconnectSSH,
  fetchSSHFingerprint,
  fetchGlobalSSHStatus,
  fetchSSHMetrics,
} from "@/api/ssh";

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        {children}
      </QueryClientProvider>
    );
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("useSSHStatus", () => {
  it("fetches SSH status for an instance", async () => {
    const mockData = { connection_state: "connected" };
    vi.mocked(fetchSSHStatus).mockResolvedValue(mockData as never);

    const { result } = renderHook(() => useSSHStatus(1), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(mockData);
    expect(fetchSSHStatus).toHaveBeenCalledWith(1);
  });

  it("does not fetch when disabled", () => {
    renderHook(() => useSSHStatus(1, false), {
      wrapper: createWrapper(),
    });
    expect(fetchSSHStatus).not.toHaveBeenCalled();
  });
});

describe("useSSHEvents", () => {
  it("fetches SSH events for an instance", async () => {
    const mockData = { events: [{ type: "connected" }] };
    vi.mocked(fetchSSHEvents).mockResolvedValue(mockData as never);

    const { result } = renderHook(() => useSSHEvents(1), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(mockData);
    expect(fetchSSHEvents).toHaveBeenCalledWith(1);
  });

  it("does not fetch when disabled", () => {
    renderHook(() => useSSHEvents(1, false), {
      wrapper: createWrapper(),
    });
    expect(fetchSSHEvents).not.toHaveBeenCalled();
  });
});

describe("useSSHTest", () => {
  it("calls testSSHConnection on mutate", async () => {
    const mockResult = { success: true, latency_ms: 10 };
    vi.mocked(testSSHConnection).mockResolvedValue(mockResult as never);

    const { result } = renderHook(() => useSSHTest(1), {
      wrapper: createWrapper(),
    });

    result.current.mutate();

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(testSSHConnection).toHaveBeenCalledWith(1);
    expect(result.current.data).toEqual(mockResult);
  });
});

describe("useSSHReconnect", () => {
  it("calls reconnectSSH on mutate", async () => {
    const mockResult = { success: true, message: "OK" };
    vi.mocked(reconnectSSH).mockResolvedValue(mockResult as never);

    const { result } = renderHook(() => useSSHReconnect(1), {
      wrapper: createWrapper(),
    });

    result.current.mutate();

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(reconnectSSH).toHaveBeenCalledWith(1);
  });
});

describe("useSSHFingerprint", () => {
  it("fetches SSH fingerprint for an instance", async () => {
    const mockData = { fingerprint: "SHA256:abc", algorithm: "ed25519" };
    vi.mocked(fetchSSHFingerprint).mockResolvedValue(mockData as never);

    const { result } = renderHook(() => useSSHFingerprint(1), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(mockData);
    expect(fetchSSHFingerprint).toHaveBeenCalledWith(1);
  });

  it("does not fetch when disabled", () => {
    renderHook(() => useSSHFingerprint(1, false), {
      wrapper: createWrapper(),
    });
    expect(fetchSSHFingerprint).not.toHaveBeenCalled();
  });
});

describe("useGlobalSSHStatus", () => {
  it("fetches global SSH status", async () => {
    const mockData = { instances: [], total_count: 0 };
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockData as never);

    const { result } = renderHook(() => useGlobalSSHStatus(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(mockData);
    expect(fetchGlobalSSHStatus).toHaveBeenCalled();
  });
});

describe("useSSHMetrics", () => {
  it("fetches SSH metrics", async () => {
    const mockData = {
      uptime_buckets: [],
      health_rates: [],
      reconnection_counts: [],
    };
    vi.mocked(fetchSSHMetrics).mockResolvedValue(mockData as never);

    const { result } = renderHook(() => useSSHMetrics(), {
      wrapper: createWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(mockData);
    expect(fetchSSHMetrics).toHaveBeenCalled();
  });
});
