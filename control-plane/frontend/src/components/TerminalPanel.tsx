import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Wifi, WifiOff, Loader2, RefreshCw } from "lucide-react";
import type { TerminalConnectionState } from "@/hooks/useTerminal";
import "@xterm/xterm/css/xterm.css";

interface TerminalPanelProps {
  connectionState: TerminalConnectionState;
  onData: (data: string) => void;
  onResize: (size: { cols: number; rows: number }) => void;
  setTerminal: (term: Terminal, fitAddon: FitAddon) => void;
  reconnect: () => void;
}

function ConnectionIndicator({ state }: { state: TerminalConnectionState }) {
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

export default function TerminalPanel({
  connectionState,
  onData,
  onResize,
  setTerminal,
  reconnect,
}: TerminalPanelProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace",
      theme: {
        background: "#111827", // gray-900
        foreground: "#e5e7eb", // gray-200
        cursor: "#e5e7eb",
        selectionBackground: "#374151", // gray-700
      },
    });

    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();

    term.loadAddon(fitAddon);
    term.loadAddon(webLinksAddon);
    term.open(containerRef.current);

    // Small delay to ensure container dimensions are available
    requestAnimationFrame(() => {
      fitAddon.fit();
    });

    term.onData(onData);
    term.onResize(onResize);

    setTerminal(term, fitAddon);
    termRef.current = term;

    const observer = new ResizeObserver(() => {
      requestAnimationFrame(() => {
        fitAddon.fit();
      });
    });
    observer.observe(containerRef.current);

    return () => {
      observer.disconnect();
      term.dispose();
      termRef.current = null;
    };
  }, []); // Mount once

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-3 py-2 bg-gray-800 border-b border-gray-700">
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
        <ConnectionIndicator state={connectionState} />
      </div>
      <div
        ref={containerRef}
        className="flex-1 bg-gray-900"
        style={{ padding: "4px" }}
      />
    </div>
  );
}
