import { useCallback, useEffect, useRef, useState } from "react";
import RFBClass from "@novnc/novnc";

export type VncConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error";

export function useVnc(instanceId: number, enabled: boolean) {
  const [connectionState, setConnectionState] =
    useState<VncConnectionState>("disconnected");
  const rfbRef = useRef<RFBClass | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const enabledRef = useRef(enabled);
  const vncClipboardRef = useRef<string>("");

  useEffect(() => {
    enabledRef.current = enabled;
  }, [enabled]);

  const disconnect = useCallback(() => {
    if (rfbRef.current) {
      rfbRef.current.disconnect();
      rfbRef.current = null;
    }
    setConnectionState("disconnected");
  }, []);

  const connect = useCallback(() => {
    if (rfbRef.current) {
      rfbRef.current.disconnect();
      rfbRef.current = null;
    }

    if (!containerRef.current) return;

    // Clear any leftover canvas from previous connection
    containerRef.current.innerHTML = "";

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${protocol}//${window.location.host}/api/v1/instances/${instanceId}/vnc/chrome/websockify`;

    setConnectionState("connecting");

    const rfb = new RFBClass(containerRef.current, wsUrl, { shared: true });
    rfb.scaleViewport = true;
    rfb.resizeSession = false;
    rfbRef.current = rfb;

    rfb.addEventListener("connect", () => {
      setConnectionState("connected");
    });

    rfb.addEventListener("clipboard", (e) => {
      vncClipboardRef.current = e.detail.text;
    });

    rfb.addEventListener("disconnect", (e) => {
      rfbRef.current = null;
      if (e.detail.clean) {
        setConnectionState("disconnected");
      } else {
        setConnectionState("error");
      }
    });
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

  const setContainer = useCallback(
    (el: HTMLDivElement | null) => {
      containerRef.current = el;
    },
    [],
  );

  const reconnect = useCallback(() => {
    disconnect();
    // Small delay to let cleanup finish
    requestAnimationFrame(() => {
      connect();
    });
  }, [connect, disconnect]);

  const copyFromVnc = useCallback(async () => {
    if (vncClipboardRef.current) {
      await navigator.clipboard.writeText(vncClipboardRef.current);
    }
  }, []);

  const pasteToVnc = useCallback(async () => {
    if (!rfbRef.current) return;
    const text = await navigator.clipboard.readText();
    if (text) {
      // Sync local clipboard to remote VNC clipboard
      rfbRef.current.clipboardPasteFrom(text);
      // Send Ctrl+V to trigger paste in the remote session
      const rfb = rfbRef.current;
      rfb.sendKey(0xffe3, "ControlLeft", true);  // Ctrl down
      rfb.sendKey(0x0076, "KeyV", true);          // V down
      rfb.sendKey(0x0076, "KeyV", false);          // V up
      rfb.sendKey(0xffe3, "ControlLeft", false);  // Ctrl up
    }
  }, []);

  return {
    connectionState,
    setContainer,
    reconnect,
    copyFromVnc,
    pasteToVnc,
  };
}
