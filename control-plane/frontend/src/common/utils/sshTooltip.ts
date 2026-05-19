import type { SSHStatusResponse } from "@common/types/ssh";

export function buildSSHTooltip(status: SSHStatusResponse | undefined): string | undefined {
  if (!status) return undefined;

  const lines: string[] = [];

  lines.push(`SSH: ${status.state}`);

  if (status.recent_events.length > 0) {
    const last = status.recent_events[status.recent_events.length - 1];
    if (last.reason) {
      lines.push(last.reason);
    }
  }

  if (status.tunnels.length > 0) {
    const summary = status.tunnels.map((t) => `${t.label}: ${t.status}`).join(", ");
    lines.push(summary);
  }

  return lines.join("\n");
}
