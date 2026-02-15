import { useCallback, useRef } from "react";
import { Wifi, WifiOff, Loader2, RefreshCw, Maximize, Copy, ClipboardPaste, ExternalLink, MessageSquare } from "lucide-react";
import type { VncConnectionState } from "@/hooks/useVnc";

interface VncPanelProps {
  instanceId: number;
  connectionState: VncConnectionState;
  setContainer: (el: HTMLDivElement | null) => void;
  reconnect: () => void;
  copyFromVnc: () => Promise<void>;
  pasteToVnc: () => Promise<void>;
  chatOpen?: boolean;
  onChatToggle?: () => void;
  showNewWindow?: boolean;
}

function ConnectionIndicator({ state }: { state: VncConnectionState }) {
  switch (state) {
    case "connected":
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <Wifi size={12} className="text-green-400" /> Connected
        </span>
      );
    case "connecting":
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <Loader2 size={12} className="text-yellow-400 animate-spin" />{" "}
          Connecting
        </span>
      );
    case "error":
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <WifiOff size={12} className="text-red-400" /> Error
        </span>
      );
    default:
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <WifiOff size={12} className="text-red-400" /> Disconnected
        </span>
      );
  }
}

export default function VncPanel({
  instanceId,
  connectionState,
  setContainer,
  reconnect,
  copyFromVnc,
  pasteToVnc,
  chatOpen,
  onChatToggle,
  showNewWindow = true,
}: VncPanelProps) {
  const panelRef = useRef<HTMLDivElement>(null);

  const toggleFullscreen = useCallback(() => {
    if (!panelRef.current) return;
    if (document.fullscreenElement) {
      document.exitFullscreen();
    } else {
      panelRef.current.requestFullscreen();
    }
  }, []);

  return (
    <div ref={panelRef} className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-3 py-2 bg-gray-800 border-b border-gray-700">
        {onChatToggle && (
          <button
            onClick={onChatToggle}
            className={`flex items-center gap-1 px-1.5 py-1 text-xs rounded ${chatOpen ? "text-blue-400" : "text-gray-400 hover:text-white"}`}
            title="Toggle chat"
          >
            <MessageSquare size={14} /> Chat
          </button>
        )}
        {(connectionState === "disconnected" || connectionState === "error") && (
          <button
            onClick={reconnect}
            className="p-1 text-gray-400 hover:text-white rounded"
            title="Reconnect"
          >
            <RefreshCw size={14} />
          </button>
        )}
        <button
          onClick={copyFromVnc}
          disabled={connectionState !== "connected"}
          className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded disabled:opacity-30 disabled:cursor-not-allowed"
          title="Copy from remote clipboard"
        >
          <Copy size={14} /> Copy
        </button>
        <button
          onClick={pasteToVnc}
          disabled={connectionState !== "connected"}
          className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded disabled:opacity-30 disabled:cursor-not-allowed"
          title="Paste to remote clipboard"
        >
          <ClipboardPaste size={14} /> Paste
        </button>
        <div className="flex-1" />
        {showNewWindow && (
          <button
            onClick={() => window.open(`/instances/${instanceId}/vnc`, "_blank", "noopener")}
            className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded"
            title="Open in new window"
          >
            <ExternalLink size={14} /> New Window
          </button>
        )}
        <button
          onClick={toggleFullscreen}
          className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded"
          title="Toggle fullscreen"
        >
          <Maximize size={14} /> Full Screen
        </button>
        <ConnectionIndicator state={connectionState} />
      </div>
      <div
        ref={setContainer}
        className="flex-1 bg-gray-900 overflow-hidden"
      />
    </div>
  );
}
