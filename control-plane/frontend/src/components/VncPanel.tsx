import { useCallback, useRef, type Ref } from "react";
import { Wifi, WifiOff, Loader2, RefreshCw, Maximize, ExternalLink, Copy, ClipboardPaste } from "lucide-react";
import type { DesktopConnectionState } from "@/hooks/useDesktop";

interface VncPanelProps {
  instanceId: number;
  connectionState: DesktopConnectionState;
  containerRef: Ref<HTMLDivElement>;
  reconnect: () => void;
  copyFromRemote: () => void;
  pasteToRemote: () => void;
  showNewWindow?: boolean;
  showFullscreen?: boolean;
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
    case "starting":
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <Loader2 size={12} className="text-yellow-400 animate-spin" /> Starting
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
  containerRef,
  reconnect,
  copyFromRemote,
  pasteToRemote,
  showNewWindow = true,
  showFullscreen = true,
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
    <div ref={panelRef} className="flex flex-col absolute inset-0">
      {/* Fixed toolbar height (h-9 + py-2 content) so the bar doesn't shift
          by ~1–2px between states as buttons swap in/out. All toolbar buttons
          use the same h-6 so the row height matches the always-visible
          New Window / Full Screen buttons. */}
      <div className="flex items-center gap-2 px-3 h-9 bg-gray-800 border-b border-gray-700">
        {(connectionState === "disconnected" || connectionState === "error") && (
          <button
            onClick={reconnect}
            className="flex items-center gap-1 h-6 px-1.5 text-xs text-gray-400 hover:text-white rounded"
            title="Reconnect"
          >
            <RefreshCw size={14} />
          </button>
        )}
        {connectionState === "connected" && (
          <>
            <button
              onClick={copyFromRemote}
              className="flex items-center gap-1 h-6 px-1.5 text-xs text-gray-400 hover:text-white rounded"
              title="Copy selected text from remote desktop to clipboard"
            >
              <Copy size={14} /> Copy
            </button>
            <button
              onClick={pasteToRemote}
              className="flex items-center gap-1 h-6 px-1.5 text-xs text-gray-400 hover:text-white rounded"
              title="Paste clipboard content into remote desktop"
            >
              <ClipboardPaste size={14} /> Paste
            </button>
          </>
        )}
        <div className="flex-1" />
        {showNewWindow && (
          <button
            onClick={() => window.open(`/instances/${instanceId}/vnc`, "_blank", "noopener")}
            className="flex items-center gap-1 h-6 px-1.5 text-xs text-gray-400 hover:text-white rounded"
            title="Open in new window"
          >
            <ExternalLink size={14} /> New Window
          </button>
        )}
        {showFullscreen && (
          <button
            onClick={toggleFullscreen}
            className="flex items-center gap-1 h-6 px-1.5 text-xs text-gray-400 hover:text-white rounded"
            title="Toggle fullscreen"
          >
            <Maximize size={14} /> Full Screen
          </button>
        )}
        <ConnectionIndicator state={connectionState} />
      </div>
      <div
        ref={containerRef}
        className="flex-1 w-full bg-gray-900 min-h-0 overflow-hidden"
      />
    </div>
  );
}
