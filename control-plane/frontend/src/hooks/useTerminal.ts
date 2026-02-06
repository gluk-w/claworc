import { useCallback, useEffect, useRef, useState } from "react";
import type { Terminal } from "@xterm/xterm";
import type { FitAddon } from "@xterm/addon-fit";

export type TerminalConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error";

export function useTerminal(instanceId: number, enabled: boolean) {
  const [connectionState, setConnectionState] =
    useState<TerminalConnectionState>("disconnected");
  const wsRef = useRef<WebSocket | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const enabledRef = useRef(enabled);

  useEffect(() => {
    enabledRef.current = enabled;
  }, [enabled]);

  const disconnect = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    setConnectionState("disconnected");
  }, []);

  const connect = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${window.location.host}/api/v1/instances/${instanceId}/terminal`;

    setConnectionState("connecting");

    const ws = new WebSocket(url);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setConnectionState("connected");

      // Send initial resize
      if (fitAddonRef.current && termRef.current) {
        const dims = fitAddonRef.current.proposeDimensions();
        if (dims) {
          ws.send(
            JSON.stringify({ type: "resize", cols: dims.cols, rows: dims.rows }),
          );
        }
      }
    };

    ws.onmessage = (event) => {
      if (termRef.current && event.data instanceof ArrayBuffer) {
        termRef.current.write(new Uint8Array(event.data));
      }
    };

    ws.onclose = (event) => {
      wsRef.current = null;
      if (event.code >= 4000 && event.code < 5000) {
        setConnectionState("error");
      } else {
        setConnectionState("disconnected");
      }
    };

    ws.onerror = () => {
      // onclose will fire after this
    };
  }, [instanceId]);

  useEffect(() => {
    if (enabled) {
      connect();
    } else {
      disconnect();
    }
    return () => {
      disconnect();
    };
  }, [enabled, connect, disconnect]);

  const onData = useCallback((data: string) => {
    if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
      const encoder = new TextEncoder();
      wsRef.current.send(encoder.encode(data));
    }
  }, []);

  const onResize = useCallback(
    (size: { cols: number; rows: number }) => {
      if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
        wsRef.current.send(
          JSON.stringify({ type: "resize", cols: size.cols, rows: size.rows }),
        );
      }
    },
    [],
  );

  const setTerminal = useCallback(
    (term: Terminal, fitAddon: FitAddon) => {
      termRef.current = term;
      fitAddonRef.current = fitAddon;
    },
    [],
  );

  const reconnect = useCallback(() => {
    // Clear terminal content for fresh session
    if (termRef.current) {
      termRef.current.clear();
    }
    connect();
  }, [connect]);

  return {
    connectionState,
    onData,
    onResize,
    setTerminal,
    reconnect,
  };
}
