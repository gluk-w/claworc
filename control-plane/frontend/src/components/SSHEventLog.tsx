import { useState, useEffect, useRef } from "react";
import { ChevronDown, Filter } from "lucide-react";
import type { SSHEventEntry } from "@/types/ssh";

/** Map event types to severity category and display style. */
const eventStyles: Record<string, { bg: string; text: string; dot: string; severity: string }> = {
  connected:          { bg: "bg-green-50",  text: "text-green-700",  dot: "bg-green-500",  severity: "info" },
  reconnected:        { bg: "bg-green-50",  text: "text-green-700",  dot: "bg-green-500",  severity: "info" },
  key_uploaded:       { bg: "bg-blue-50",   text: "text-blue-700",   dot: "bg-blue-500",   severity: "info" },
  disconnected:       { bg: "bg-yellow-50", text: "text-yellow-700", dot: "bg-yellow-500", severity: "warning" },
  reconnecting:       { bg: "bg-yellow-50", text: "text-yellow-700", dot: "bg-yellow-500", severity: "warning" },
  reconnect_failed:   { bg: "bg-red-50",    text: "text-red-700",    dot: "bg-red-500",    severity: "error" },
  health_check_failed:{ bg: "bg-red-50",    text: "text-red-700",    dot: "bg-red-500",    severity: "error" },
};

const defaultStyle = { bg: "bg-gray-50", text: "text-gray-700", dot: "bg-gray-400", severity: "info" };

function formatTimestamp(ts: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function formatEventType(type: string): string {
  return type
    .split("_")
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

interface SSHEventLogProps {
  events: SSHEventEntry[] | undefined;
  isLoading: boolean;
  isError: boolean;
}

export default function SSHEventLog({ events, isLoading, isError }: SSHEventLogProps) {
  const [filterType, setFilterType] = useState<string>("all");
  const [filterOpen, setFilterOpen] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const prevCountRef = useRef(0);

  // Collect unique event types for filter dropdown
  const eventTypes = events
    ? Array.from(new Set(events.map((e) => e.type))).sort()
    : [];

  const filtered = events
    ? filterType === "all"
      ? events
      : events.filter((e) => e.type === filterType)
    : [];

  // Auto-scroll to bottom when new events arrive
  useEffect(() => {
    if (filtered.length > prevCountRef.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
    prevCountRef.current = filtered.length;
  }, [filtered.length]);

  if (isLoading && !events) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="text-sm text-gray-500">Loading event history...</div>
      </div>
    );
  }

  if (isError && !events) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="text-sm text-red-600">Failed to load event history.</div>
      </div>
    );
  }

  return (
    <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
      <div className="px-6 py-4 border-b border-gray-200 flex items-center justify-between">
        <h3 className="text-sm font-medium text-gray-900">Connection Events</h3>

        {/* Filter dropdown */}
        <div className="relative">
          <button
            onClick={() => setFilterOpen((p) => !p)}
            className="flex items-center gap-1.5 text-xs text-gray-600 hover:text-gray-900 px-2 py-1 rounded border border-gray-200 hover:border-gray-300"
          >
            <Filter size={12} />
            {filterType === "all" ? "All events" : formatEventType(filterType)}
            <ChevronDown size={12} />
          </button>
          {filterOpen && (
            <div className="absolute right-0 mt-1 w-48 bg-white border border-gray-200 rounded-md shadow-lg z-10">
              <button
                onClick={() => { setFilterType("all"); setFilterOpen(false); }}
                className={`block w-full text-left px-3 py-1.5 text-xs hover:bg-gray-50 ${
                  filterType === "all" ? "font-medium text-blue-600" : "text-gray-700"
                }`}
              >
                All events
              </button>
              {eventTypes.map((t) => (
                <button
                  key={t}
                  onClick={() => { setFilterType(t); setFilterOpen(false); }}
                  className={`block w-full text-left px-3 py-1.5 text-xs hover:bg-gray-50 ${
                    filterType === t ? "font-medium text-blue-600" : "text-gray-700"
                  }`}
                >
                  {formatEventType(t)}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Timeline */}
      <div ref={scrollRef} className="max-h-72 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="px-6 py-8 text-center text-sm text-gray-400">
            No events recorded.
          </div>
        ) : (
          <div className="divide-y divide-gray-100">
            {filtered.map((event, idx) => {
              const style = eventStyles[event.type] ?? defaultStyle;
              return (
                <div key={`${event.timestamp}-${idx}`} className="flex items-start gap-3 px-6 py-3">
                  {/* Timeline dot */}
                  <div className="pt-1.5 flex-shrink-0">
                    <span className={`block w-2 h-2 rounded-full ${style.dot}`} />
                  </div>

                  {/* Event content */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span
                        className={`inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium ${style.bg} ${style.text}`}
                      >
                        {formatEventType(event.type)}
                      </span>
                      <span className="text-xs text-gray-400">
                        {formatTimestamp(event.timestamp)}
                      </span>
                    </div>
                    {event.details && (
                      <p className="text-xs text-gray-500 mt-0.5 truncate">{event.details}</p>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
