import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  fetchSSHStatus,
  fetchSSHEvents,
  testSSHConnection,
  reconnectSSH,
  fetchSSHFingerprint,
  fetchGlobalSSHStatus,
  fetchSSHMetrics,
} from "../ssh";

vi.mock("../client", () => ({
  default: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

import client from "../client";

beforeEach(() => {
  vi.clearAllMocks();
});

describe("SSH API functions", () => {
  describe("fetchSSHStatus", () => {
    it("calls GET /instances/{id}/ssh-status", async () => {
      const mockData = { connection_state: "connected" };
      vi.mocked(client.get).mockResolvedValue({ data: mockData });

      const result = await fetchSSHStatus(42);

      expect(client.get).toHaveBeenCalledWith("/instances/42/ssh-status");
      expect(result).toEqual(mockData);
    });
  });

  describe("fetchSSHEvents", () => {
    it("calls GET /instances/{id}/ssh-events", async () => {
      const mockData = { events: [] };
      vi.mocked(client.get).mockResolvedValue({ data: mockData });

      const result = await fetchSSHEvents(7);

      expect(client.get).toHaveBeenCalledWith("/instances/7/ssh-events");
      expect(result).toEqual(mockData);
    });
  });

  describe("testSSHConnection", () => {
    it("calls POST /instances/{id}/ssh-test", async () => {
      const mockData = { success: true, latency_ms: 10 };
      vi.mocked(client.post).mockResolvedValue({ data: mockData });

      const result = await testSSHConnection(3);

      expect(client.post).toHaveBeenCalledWith("/instances/3/ssh-test");
      expect(result).toEqual(mockData);
    });
  });

  describe("reconnectSSH", () => {
    it("calls POST /instances/{id}/ssh-reconnect", async () => {
      const mockData = { success: true, message: "Reconnected" };
      vi.mocked(client.post).mockResolvedValue({ data: mockData });

      const result = await reconnectSSH(5);

      expect(client.post).toHaveBeenCalledWith("/instances/5/ssh-reconnect");
      expect(result).toEqual(mockData);
    });
  });

  describe("fetchSSHFingerprint", () => {
    it("calls GET /instances/{id}/ssh-fingerprint", async () => {
      const mockData = {
        fingerprint: "SHA256:abc",
        algorithm: "ssh-ed25519",
      };
      vi.mocked(client.get).mockResolvedValue({ data: mockData });

      const result = await fetchSSHFingerprint(1);

      expect(client.get).toHaveBeenCalledWith(
        "/instances/1/ssh-fingerprint",
      );
      expect(result).toEqual(mockData);
    });
  });

  describe("fetchGlobalSSHStatus", () => {
    it("calls GET /ssh-status", async () => {
      const mockData = { instances: [], total_count: 0 };
      vi.mocked(client.get).mockResolvedValue({ data: mockData });

      const result = await fetchGlobalSSHStatus();

      expect(client.get).toHaveBeenCalledWith("/ssh-status");
      expect(result).toEqual(mockData);
    });
  });

  describe("fetchSSHMetrics", () => {
    it("calls GET /ssh-metrics", async () => {
      const mockData = {
        uptime_buckets: [],
        health_rates: [],
        reconnection_counts: [],
      };
      vi.mocked(client.get).mockResolvedValue({ data: mockData });

      const result = await fetchSSHMetrics();

      expect(client.get).toHaveBeenCalledWith("/ssh-metrics");
      expect(result).toEqual(mockData);
    });
  });

  describe("error handling", () => {
    it("propagates network errors from fetchSSHStatus", async () => {
      vi.mocked(client.get).mockRejectedValue(new Error("Network error"));
      await expect(fetchSSHStatus(1)).rejects.toThrow("Network error");
    });

    it("propagates errors from testSSHConnection", async () => {
      vi.mocked(client.post).mockRejectedValue(new Error("Timeout"));
      await expect(testSSHConnection(1)).rejects.toThrow("Timeout");
    });

    it("propagates errors from reconnectSSH", async () => {
      vi.mocked(client.post).mockRejectedValue(new Error("Forbidden"));
      await expect(reconnectSSH(1)).rejects.toThrow("Forbidden");
    });
  });
});
