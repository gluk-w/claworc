import { useState, useCallback, useRef } from "react";

export type DesktopConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error";

export function useDesktop(instanceId: number, enabled: boolean) {
  const [connectionState, setConnectionState] =
    useState<DesktopConnectionState>(enabled ? "connecting" : "disconnected");
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const desktopUrl = `/api/v1/instances/${instanceId}/desktop/`;

  const setIframe = useCallback(
    (el: HTMLIFrameElement | null) => {
      iframeRef.current = el;
      if (el && enabled) {
        setConnectionState("connecting");
      }
    },
    [enabled],
  );

  const onLoad = useCallback(() => {
    if (enabled) {
      setConnectionState("connected");
    }
  }, [enabled]);

  const onError = useCallback(() => {
    setConnectionState("error");
  }, []);

  const reconnect = useCallback(() => {
    setConnectionState("connecting");
    setReloadKey((k) => k + 1);
    if (iframeRef.current) {
      iframeRef.current.src = desktopUrl + `?_=${Date.now()}`;
    }
  }, [desktopUrl]);

  // Sync enabled flag with connection state
  if (!enabled && connectionState !== "disconnected") {
    setConnectionState("disconnected");
  } else if (
    enabled &&
    connectionState === "disconnected" &&
    iframeRef.current
  ) {
    setConnectionState("connecting");
  }

  return {
    connectionState,
    desktopUrl: enabled ? desktopUrl : "",
    setIframe,
    onLoad,
    onError,
    reconnect,
    reloadKey,
  };
}
