const statusStyles: Record<string, string> = {
  running: "bg-green-100 text-green-800",
  creating: "bg-yellow-100 text-yellow-800",
  restarting: "bg-orange-100 text-orange-800",
  stopping: "bg-yellow-100 text-yellow-800",
  stopped: "bg-gray-100 text-gray-800",
  error: "bg-red-100 text-red-800",
  failed: "bg-red-100 text-red-800",
};

export default function StatusBadge({ status, tooltip }: { status: string; tooltip?: string }) {
  const style = statusStyles[status] ?? "bg-gray-100 text-gray-800";
  const badge = (
    <span
      data-testid="status-badge"
      className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${style}`}
    >
      {status}
    </span>
  );

  if (!tooltip) return badge;

  return (
    <span className="relative group">
      {badge}
      <span className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-2 whitespace-pre rounded bg-gray-900 px-2.5 py-1.5 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100 z-50">
        {tooltip}
      </span>
    </span>
  );
}
