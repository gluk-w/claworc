import { useEffect, useRef } from "react";
import { Pause, Play, Trash2, Wifi, WifiOff } from "lucide-react";

interface LogViewerProps {
  logs: string[];
  isPaused: boolean;
  isConnected: boolean;
  onTogglePause: () => void;
  onClear: () => void;
}

export default function LogViewer({
  logs,
  isPaused,
  isConnected,
  onTogglePause,
  onClear,
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
          <div className="text-gray-500">Waiting for logs...</div>
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
