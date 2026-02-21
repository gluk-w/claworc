import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SSHTroubleshoot from "../SSHTroubleshoot";
import { renderWithProviders } from "@/test/helpers";

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
  testSSHConnection,
  reconnectSSH,
  fetchSSHFingerprint,
} from "@/api/ssh";

beforeEach(() => {
  vi.clearAllMocks();
});

describe("SSHTroubleshoot", () => {
  it("renders the dialog with all sections", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:abcdef1234567890",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    expect(screen.getByText("SSH Troubleshooting")).toBeInTheDocument();
    expect(screen.getByText("Connection Test")).toBeInTheDocument();
    expect(screen.getByText("Force Reconnect")).toBeInTheDocument();
    expect(screen.getByText("SSH Key Fingerprint")).toBeInTheDocument();
    expect(screen.getByText("Troubleshooting Tips")).toBeInTheDocument();
    expect(screen.getByText("Close")).toBeInTheDocument();
  });

  it("displays fingerprint when loaded", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:abcdef1234567890",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await waitFor(() => {
      expect(
        screen.getByText("SHA256:abcdef1234567890"),
      ).toBeInTheDocument();
    });
    expect(screen.getByText("ssh-ed25519")).toBeInTheDocument();
  });

  it("shows verified status when fingerprint is verified", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:verified123",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await waitFor(() => {
      expect(screen.getByText("Verified")).toBeInTheDocument();
    });
  });

  it("shows mismatch warning when fingerprint is not verified", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:mismatch456",
      algorithm: "ssh-ed25519",
      verified: false,
    });
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await waitFor(() => {
      expect(
        screen.getByText(/Fingerprint mismatch/),
      ).toBeInTheDocument();
    });
  });

  it("runs connection test and shows success result", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:test",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    vi.mocked(testSSHConnection).mockResolvedValue({
      success: true,
      latency_ms: 42,
      tunnel_status: [{ service: "VNC", healthy: true }],
      command_test: true,
    });

    const user = userEvent.setup();
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await user.click(screen.getByText("Run Test"));

    await waitFor(() => {
      expect(screen.getByText("Connection OK")).toBeInTheDocument();
    });
    expect(screen.getByText("Latency: 42ms")).toBeInTheDocument();
    expect(screen.getByText(/Command execution:.*OK/)).toBeInTheDocument();
    expect(screen.getByText(/VNC:.*Healthy/)).toBeInTheDocument();
  });

  it("runs connection test and shows failure result", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:test",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    vi.mocked(testSSHConnection).mockResolvedValue({
      success: false,
      latency_ms: 0,
      tunnel_status: [
        { service: "VNC", healthy: false, error: "port closed" },
      ],
      command_test: false,
      error: "SSH connection refused",
    });

    const user = userEvent.setup();
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await user.click(screen.getByText("Run Test"));

    await waitFor(() => {
      expect(screen.getByText("Connection Failed")).toBeInTheDocument();
    });
    expect(screen.getByText("SSH connection refused")).toBeInTheDocument();
    expect(screen.getByText(/VNC:.*port closed/)).toBeInTheDocument();
  });

  it("handles connection test network error", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:test",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    vi.mocked(testSSHConnection).mockRejectedValue({
      response: { data: { detail: "Server unreachable" } },
    });

    const user = userEvent.setup();
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await user.click(screen.getByText("Run Test"));

    await waitFor(() => {
      expect(screen.getByText("Server unreachable")).toBeInTheDocument();
    });
  });

  it("triggers reconnect and shows success message", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:test",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    vi.mocked(reconnectSSH).mockResolvedValue({
      success: true,
      message: "SSH reconnection initiated",
    });

    const user = userEvent.setup();
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await user.click(screen.getByText("Reconnect"));

    await waitFor(() => {
      expect(
        screen.getByText("SSH reconnection initiated"),
      ).toBeInTheDocument();
    });
  });

  it("triggers reconnect and shows error message", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:test",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    vi.mocked(reconnectSSH).mockRejectedValue({
      response: { data: { detail: "Instance not running" } },
    });

    const user = userEvent.setup();
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await user.click(screen.getByText("Reconnect"));

    await waitFor(() => {
      expect(screen.getByText("Instance not running")).toBeInTheDocument();
    });
  });

  it("calls onClose when close button is clicked", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue({
      fingerprint: "SHA256:test",
      algorithm: "ssh-ed25519",
      verified: true,
    });
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={onClose} />,
    );

    await user.click(screen.getByText("Close"));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("shows all troubleshooting tips", () => {
    vi.mocked(fetchSSHFingerprint).mockReturnValue(new Promise(() => {}));
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    expect(
      screen.getByText(/stuck in "reconnecting"/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/High latency/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/tunnels are unhealthy/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/SSH key fingerprint/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Connection Events log/),
    ).toBeInTheDocument();
  });

  it("shows fingerprint not available when no data", async () => {
    vi.mocked(fetchSSHFingerprint).mockResolvedValue(
      undefined as unknown as { fingerprint: string; algorithm: string; verified: boolean },
    );
    renderWithProviders(
      <SSHTroubleshoot instanceId={1} onClose={vi.fn()} />,
    );

    await waitFor(() => {
      expect(
        screen.getByText("Fingerprint not available."),
      ).toBeInTheDocument();
    });
  });
});
