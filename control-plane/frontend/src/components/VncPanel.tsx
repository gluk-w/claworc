import { useCallback, useRef } from "react";
import { Wifi, WifiOff, Loader2, RefreshCw, Maximize, ExternalLink, MessageSquare } from "lucide-react";
import type { DesktopConnectionState } from "@/hooks/useDesktop";

interface VncPanelProps {
  instanceId: number;
  connectionState: DesktopConnectionState;
  desktopUrl: string;
  setIframe: (el: HTMLIFrameElement | null) => void;
  onLoad: () => void;
  onError: () => void;
  reconnect: () => void;
  chatOpen?: boolean;
  onChatToggle?: () => void;
  showNewWindow?: boolean;
}

function ConnectionIndicator({ state }: { state: DesktopConnectionState }) {
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
  desktopUrl,
  setIframe,
  onLoad,
  onError,
  reconnect,
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
      <iframe
        ref={setIframe}
        src={connectionState !== "disconnected" ? desktopUrl : undefined}
        onLoad={onLoad}
        onError={onError}
        className="flex-1 w-full border-0 bg-gray-900"
        allow="clipboard-read; clipboard-write"
      />
    </div>
  );
}
