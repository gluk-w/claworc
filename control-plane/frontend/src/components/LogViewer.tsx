import { useEffect, useRef } from "react";
import { Info, Pause, Play, Trash2, Wifi, WifiOff } from "lucide-react";
import type { LogType } from "@/hooks/useInstanceLogs";

interface LogViewerProps {
  logs: string[];
  isPaused: boolean;
  isConnected: boolean;
  onTogglePause: () => void;
  onClear: () => void;
  logType: LogType;
  onLogTypeChange: (type: LogType) => void;
}

export default function LogViewer({
  logs,
  isPaused,
  isConnected,
  onTogglePause,
  onClear,
  logType,
  onLogTypeChange,
}: LogViewerProps) {
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!isPaused && bottomRef.current) {
      bottomRef.current.scrollIntoView({ behavior: "smooth" });
    }
  }, [logs, isPaused]);

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-3 py-2 bg-gray-800 border-b border-gray-700">
        <button
          onClick={onTogglePause}
          className="p-1 text-gray-400 hover:text-white rounded"
          title={isPaused ? "Resume" : "Pause"}
        >
          {isPaused ? <Play size={14} /> : <Pause size={14} />}
        </button>
        <button
          onClick={onClear}
          className="p-1 text-gray-400 hover:text-white rounded"
          title="Clear"
        >
          <Trash2 size={14} />
        </button>
        <div className="flex items-center gap-0.5 ml-2">
          <button
            onClick={() => onLogTypeChange("runtime")}
            className={`px-2 py-0.5 text-xs rounded-l font-medium transition-colors ${
              logType === "runtime"
                ? "bg-gray-600 text-white"
                : "bg-gray-700 text-gray-400 hover:text-gray-200"
            }`}
          >
            Runtime
          </button>
          <button
            onClick={() => onLogTypeChange("creation")}
            className={`px-2 py-0.5 text-xs rounded-r font-medium transition-colors ${
              logType === "creation"
                ? "bg-gray-600 text-white"
                : "bg-gray-700 text-gray-400 hover:text-gray-200"
            }`}
          >
            Creation
          </button>
        </div>
        {logType === "creation" && (
          <span
            className="text-gray-400 hover:text-gray-200 cursor-help"
            title="Creation logs are ephemeral and not stored. Switch to Runtime logs to see persistent application logs."
          >
            <Info size={14} />
          </span>
        )}
        <div className="flex-1" />
        <span className="flex items-center gap-1 text-xs text-gray-400">
          {isConnected ? (
            <>
              <Wifi size={12} className="text-green-400" /> Connected
            </>
          ) : (
            <>
              <WifiOff size={12} className="text-red-400" /> Disconnected
            </>
          )}
        </span>
      </div>
      <div className="flex-1 overflow-auto bg-gray-900 p-3 font-mono text-xs text-gray-300 min-h-[300px]">
        {logs.length === 0 ? (
          <div className="text-gray-500">
            {logType === "creation"
              ? isConnected
                ? "No creation events yet. Container may not be starting..."
                : "Waiting for container creation events..."
              : isConnected
                ? "No logs yet. The instance may still be starting..."
                : "Waiting for logs..."}
          </div>
        ) : (
          logs.map((line, i) => (
            <div key={i} className="whitespace-pre-wrap leading-5">
              {line}
            </div>
          ))
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
