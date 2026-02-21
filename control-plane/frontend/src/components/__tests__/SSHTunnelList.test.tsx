import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import SSHTunnelList from "../SSHTunnelList";
import { renderWithProviders } from "@/test/helpers";
import type { SSHTunnelStatus } from "@/types/ssh";

const mockTunnels: SSHTunnelStatus[] = [
  {
    service: "VNC",
    local_port: 5900,
    remote_port: 5900,
    created_at: "2026-02-21T10:00:00Z",
    last_check: "2026-02-21T12:00:00Z",
    bytes_transferred: 1048576,
    healthy: true,
  },
  {
    service: "Gateway",
    local_port: 8080,
    remote_port: 8080,
    created_at: "2026-02-21T10:00:00Z",
    last_check: "2026-02-21T12:00:00Z",
    last_error: "connection refused",
    bytes_transferred: 0,
    healthy: false,
  },
];

describe("SSHTunnelList", () => {
  it("renders empty state when no tunnels", () => {
    renderWithProviders(<SSHTunnelList tunnels={[]} />);
    expect(screen.getByText("No active tunnels.")).toBeInTheDocument();
  });

  it("renders table headers", () => {
    renderWithProviders(<SSHTunnelList tunnels={mockTunnels} />);
    expect(screen.getByText("Service")).toBeInTheDocument();
    expect(screen.getByText("Local Port")).toBeInTheDocument();
    expect(screen.getByText("Remote Port")).toBeInTheDocument();
    expect(screen.getByText("Status")).toBeInTheDocument();
    expect(screen.getByText("Last Check")).toBeInTheDocument();
    expect(screen.getByText("Transferred")).toBeInTheDocument();
    expect(screen.getByText("Error")).toBeInTheDocument();
  });

  it("renders tunnel data correctly", () => {
    renderWithProviders(<SSHTunnelList tunnels={mockTunnels} />);
    expect(screen.getByText("VNC")).toBeInTheDocument();
    // 5900 appears twice (local and remote for VNC)
    expect(screen.getAllByText("5900")).toHaveLength(2);
    expect(screen.getByText("Gateway")).toBeInTheDocument();
    // 8080 appears twice (local and remote for Gateway)
    expect(screen.getAllByText("8080")).toHaveLength(2);
  });

  it("renders healthy status badge", () => {
    renderWithProviders(<SSHTunnelList tunnels={mockTunnels} />);
    expect(screen.getByText("healthy")).toBeInTheDocument();
    expect(screen.getByText("unhealthy")).toBeInTheDocument();
  });

  it("renders bytes transferred formatted", () => {
    renderWithProviders(<SSHTunnelList tunnels={mockTunnels} />);
    expect(screen.getByText("1.0 MB")).toBeInTheDocument();
    expect(screen.getByText("0 B")).toBeInTheDocument();
  });

  it("renders tunnel errors", () => {
    renderWithProviders(<SSHTunnelList tunnels={mockTunnels} />);
    expect(screen.getByText("connection refused")).toBeInTheDocument();
  });

  it("renders dash for missing error", () => {
    renderWithProviders(<SSHTunnelList tunnels={[mockTunnels[0]!]} />);
    // The tunnel without an error should show a dash
    const cells = screen.getAllByText("—");
    expect(cells.length).toBeGreaterThan(0);
  });

  it("formats various byte sizes correctly", () => {
    const tunnels: SSHTunnelStatus[] = [
      {
        service: "KB-test",
        local_port: 1000,
        remote_port: 1000,
        created_at: "2026-02-21T10:00:00Z",
        bytes_transferred: 2048,
        healthy: true,
      },
      {
        service: "GB-test",
        local_port: 2000,
        remote_port: 2000,
        created_at: "2026-02-21T10:00:00Z",
        bytes_transferred: 1073741824,
        healthy: true,
      },
    ];
    renderWithProviders(<SSHTunnelList tunnels={tunnels} />);
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("1.0 GB")).toBeInTheDocument();
  });
});
