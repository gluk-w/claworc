import { useState, useCallback, useRef, useEffect } from "react";
import RFB from "@novnc/novnc";

export type DesktopConnectionState =
  | "disconnected"
  | "connecting"
  | "starting"
  | "connected"
  | "error";

// First few retries are fast — the typical failure is dialing during the
// browser pod's cold start (a few hundred ms), so a quick second try usually
// recovers. Later retries back off so we don't hammer a genuinely-broken pod.
const RETRY_DELAYS_MS = [500, 1000, 2000, 4000, 8000];

export function useDesktop(instanceId: number, enabled: boolean) {
  const [connectionState, setConnectionState] =
    useState<DesktopConnectionState>("disconnected");
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const clipboardTextRef = useRef<string>("");
  const [connectTrigger, setConnectTrigger] = useState(0);
  const retryAttemptRef = useRef(0);
  const retryTimerRef = useRef<number | null>(null);

  const clearRetryTimer = () => {
    if (retryTimerRef.current !== null) {
      window.clearTimeout(retryTimerRef.current);
      retryTimerRef.current = null;
    }
  };

  const scheduleRetry = useCallback(() => {
    if (!enabled) return;
    const i = Math.min(retryAttemptRef.current, RETRY_DELAYS_MS.length - 1);
    const delay = RETRY_DELAYS_MS[i];
    retryAttemptRef.current += 1;
    clearRetryTimer();
    retryTimerRef.current = window.setTimeout(() => {
      retryTimerRef.current = null;
      setConnectTrigger((n) => n + 1);
    }, delay);
  }, [enabled]);

  const connect = useCallback(() => {
    // Disconnect any existing session
    if (rfbRef.current) {
      try { rfbRef.current.disconnect(); } catch { /* ignore */ }
      rfbRef.current = null;
    }

    const container = containerRef.current;
    if (!container || !enabled) {
      setConnectionState("disconnected");
      return;
    }

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${proto}//${window.location.host}/api/v1/instances/${instanceId}/desktop/websockify`;

    // First attempt shows "Connecting"; any retry while the pod is still
    // warming up keeps the indicator on "Starting" so the bar doesn't flicker
    // through Connecting/Starting/Connecting on each backoff tick.
    setConnectionState(retryAttemptRef.current > 0 ? "starting" : "connecting");

    try {
      const rfb = new RFB(container, wsUrl);
      rfb.scaleViewport = true;
      rfb.resizeSession = false;
      rfb.background = "rgb(17, 24, 39)"; // gray-900

      rfb.addEventListener("connect", () => {
        retryAttemptRef.current = 0;
        clearRetryTimer();
        setConnectionState("connected");
      });

      rfb.addEventListener("disconnect", (ev: Event) => {
        const detail = (ev as CustomEvent).detail;
        rfbRef.current = null;
        if (detail?.clean) {
          // Server-initiated clean close; don't retry, just idle out.
          setConnectionState("disconnected");
        } else if (retryAttemptRef.current < RETRY_DELAYS_MS.length) {
          // Dirty disconnect — usually the browser pod still spinning up after
          // a re-enable. Show "starting" while we auto-retry so the user
          // sees a benign in-progress state instead of a scary "error".
          setConnectionState("starting");
          scheduleRetry();
        } else {
          // Retries exhausted; surface the failure so the user can act.
          setConnectionState("error");
        }
      });

      rfb.addEventListener("clipboard", (ev: Event) => {
        const text = (ev as CustomEvent).detail?.text;
        if (text) clipboardTextRef.current = text;
      });

      rfbRef.current = rfb;
    } catch {
      if (retryAttemptRef.current < RETRY_DELAYS_MS.length) {
        setConnectionState("starting");
        scheduleRetry();
      } else {
        setConnectionState("error");
      }
    }
  }, [instanceId, enabled, scheduleRetry]);

  // Connect when enabled changes or reconnect is triggered. Reset the retry
  // counter only when enabled flips (or via manual reconnect) — not on every
  // connectTrigger tick, otherwise each scheduled retry would zero the
  // counter and the indicator would flash "Connecting" for one frame before
  // settling back on "Starting".
  useEffect(() => {
    if (enabled) {
      connect();
    } else {
      clearRetryTimer();
      retryAttemptRef.current = 0;
      if (rfbRef.current) {
        try { rfbRef.current.disconnect(); } catch { /* ignore */ }
        rfbRef.current = null;
      }
      setConnectionState("disconnected");
    }

    return () => {
      clearRetryTimer();
      if (rfbRef.current) {
        try { rfbRef.current.disconnect(); } catch { /* ignore */ }
        rfbRef.current = null;
      }
    };
  }, [enabled, connect, connectTrigger]);

  // Reset retry counter when enabled flips on, so a re-enabled VNC after
  // having previously errored out starts fresh with "Connecting".
  useEffect(() => {
    if (enabled) retryAttemptRef.current = 0;
  }, [enabled]);

  const reconnect = useCallback(() => {
    retryAttemptRef.current = 0;
    clearRetryTimer();
    setConnectTrigger((n) => n + 1);
  }, []);

  const copyFromRemote = useCallback(async () => {
    if (clipboardTextRef.current) {
      await navigator.clipboard.writeText(clipboardTextRef.current);
    }
  }, []);

  const pasteToRemote = useCallback(async () => {
    try {
      const text = await navigator.clipboard.readText();
      const rfb = rfbRef.current;
      if (!rfb || !text) return;
      rfb.clipboardPasteFrom(text);
      // Simulate Ctrl+V to actually paste in the remote desktop
      rfb.sendKey(0xFFE3, "ControlLeft", true);
      rfb.sendKey(0x0076, "KeyV", true);
      rfb.sendKey(0x0076, "KeyV", false);
      rfb.sendKey(0xFFE3, "ControlLeft", false);
    } catch {
      // Clipboard read permission denied or not available
    }
  }, []);

  return {
    connectionState,
    containerRef,
    reconnect,
    copyFromRemote,
    pasteToRemote,
  };
}
