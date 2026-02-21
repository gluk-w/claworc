import { useState, useEffect, useRef } from "react";
import { RefreshCw, Filter } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { formatDistanceToNow } from "date-fns";
import { useSSHEvents } from "@/hooks/useSSH";
import type { SSHEvent } from "@/types/ssh";

type Severity = "info" | "warning" | "error";

const eventSeverity: Record<string, Severity> = {
  connected: "info",
  reconnect_success: "info",
  disconnected: "warning",
  reconnecting: "warning",
  health_check_failed: "error",
  reconnect_failed: "error",
};

const severityStyles: Record<Severity, { dot: string; text: string; bg: string }> = {
  info: { dot: "bg-green-500", text: "text-green-700", bg: "bg-green-50" },
  warning: { dot: "bg-yellow-500", text: "text-yellow-700", bg: "bg-yellow-50" },
  error: { dot: "bg-red-500", text: "text-red-700", bg: "bg-red-50" },
};

function getSeverity(eventType: string): Severity {
  return eventSeverity[eventType] ?? "info";
}

function formatEventType(type: string): string {
  return type.replace(/_/g, " ");
}

function formatTime(iso: string): string {
  if (!iso) return "—";
  try {
    return formatDistanceToNow(new Date(iso), { addSuffix: true });
  } catch {
    return "—";
  }
}

function formatTimestamp(iso: string): string {
  if (!iso) return "";
  try {
    return new Date(iso).toLocaleTimeString();
  } catch {
    return "";
  }
}

interface SSHEventLogProps {
  instanceId: number;
  enabled?: boolean;
}

export default function SSHEventLog({
  instanceId,
  enabled = true,
}: SSHEventLogProps) {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, isFetching } = useSSHEvents(
    instanceId,
    enabled,
  );
  const [filterType, setFilterType] = useState<string>("all");
  const [showFilter, setShowFilter] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const prevCountRef = useRef(0);

  const events = data?.events ?? [];

  // Get unique event types for filter
  const eventTypes = Array.from(new Set(events.map((e) => e.type)));

  const filteredEvents =
    filterType === "all"
      ? events
      : events.filter((e) => e.type === filterType);

  // Auto-scroll to bottom when new events arrive
  useEffect(() => {
    if (filteredEvents.length > prevCountRef.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
    prevCountRef.current = filteredEvents.length;
  }, [filteredEvents.length]);

  const handleRefresh = () => {
    queryClient.invalidateQueries({ queryKey: ["ssh-events", instanceId] });
  };

  if (isLoading) {
    return (
      <div className="animate-pulse space-y-3">
        <div className="h-3 bg-gray-200 rounded w-3/4" />
        <div className="h-3 bg-gray-200 rounded w-1/2" />
        <div className="h-3 bg-gray-200 rounded w-2/3" />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="flex items-center justify-between">
        <p className="text-sm text-red-600">
          Failed to load SSH events.
        </p>
        <button
          onClick={handleRefresh}
          className="text-sm text-blue-600 hover:text-blue-800"
        >
          Retry
        </button>
      </div>
    );
  }

  if (events.length === 0) {
    return (
      <p className="text-sm text-gray-500">No connection events recorded.</p>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <span className="text-xs text-gray-500">
            {filteredEvents.length} event{filteredEvents.length !== 1 ? "s" : ""}
          </span>
          <div className="relative">
            <button
              onClick={() => setShowFilter(!showFilter)}
              className={`p-1 rounded hover:bg-gray-100 ${filterType !== "all" ? "text-blue-600" : "text-gray-400 hover:text-gray-600"}`}
              title="Filter events"
            >
              <Filter size={14} />
            </button>
            {showFilter && (
              <div className="absolute left-0 top-full mt-1 bg-white border border-gray-200 rounded-md shadow-lg z-10 py-1 min-w-[160px]">
                <button
                  onClick={() => { setFilterType("all"); setShowFilter(false); }}
                  className={`block w-full text-left px-3 py-1.5 text-xs ${filterType === "all" ? "bg-blue-50 text-blue-700" : "text-gray-700 hover:bg-gray-50"}`}
                >
                  All events
                </button>
                {eventTypes.map((type) => {
                  const severity = getSeverity(type);
                  const style = severityStyles[severity];
                  return (
                    <button
                      key={type}
                      onClick={() => { setFilterType(type); setShowFilter(false); }}
                      className={`block w-full text-left px-3 py-1.5 text-xs ${filterType === type ? "bg-blue-50 text-blue-700" : "text-gray-700 hover:bg-gray-50"}`}
                    >
                      <span className="flex items-center gap-2">
                        <span className={`h-1.5 w-1.5 rounded-full ${style.dot}`} />
                        {formatEventType(type)}
                      </span>
                    </button>
                  );
                })}
              </div>
            )}
          </div>
        </div>
        <button
          onClick={handleRefresh}
          disabled={isFetching}
          className="p-1 text-gray-400 hover:text-gray-600 disabled:opacity-50"
          title="Refresh events"
        >
          <RefreshCw size={14} className={isFetching ? "animate-spin" : ""} />
        </button>
      </div>

      <div
        ref={scrollRef}
        className="max-h-64 overflow-y-auto space-y-0"
      >
        {filteredEvents.map((event, i) => (
          <EventRow key={`${event.timestamp}-${i}`} event={event} />
        ))}
      </div>
    </div>
  );
}

function EventRow({ event }: { event: SSHEvent }) {
  const severity = getSeverity(event.type);
  const style = severityStyles[severity];

  return (
    <div className={`flex items-start gap-3 px-3 py-2 ${style.bg} border-b border-gray-100 last:border-b-0`}>
      <div className="flex-shrink-0 pt-1">
        <span className={`block h-2 w-2 rounded-full ${style.dot}`} />
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-baseline gap-2">
          <span className={`text-xs font-medium capitalize ${style.text}`}>
            {formatEventType(event.type)}
          </span>
          <span className="text-xs text-gray-400" title={event.timestamp}>
            {formatTime(event.timestamp)}
          </span>
          <span className="text-xs text-gray-300">
            {formatTimestamp(event.timestamp)}
          </span>
        </div>
        {event.details && (
          <p className="text-xs text-gray-600 mt-0.5 truncate">
            {event.details}
          </p>
        )}
      </div>
    </div>
  );
}
