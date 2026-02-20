import { useCallback, useEffect, useRef, useState } from "react";

export type DesktopConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error";

export function useDesktop(instanceId: number, enabled: boolean) {
  const [connectionState, setConnectionState] =
    useState<DesktopConnectionState>("disconnected");
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const [desktopUrl, setDesktopUrl] = useState("");

  useEffect(() => {
    if (enabled) {
      setConnectionState("connecting");
      setDesktopUrl(`/api/v1/instances/${instanceId}/desktop/`);
    } else {
      setConnectionState("disconnected");
      setDesktopUrl("");
    }
  }, [instanceId, enabled]);

  const setIframe = useCallback((el: HTMLIFrameElement | null) => {
    iframeRef.current = el;
  }, []);

  const onLoad = useCallback(() => {
    if (iframeRef.current?.src) {
      setConnectionState("connected");
    }
  }, []);

  const onError = useCallback(() => {
    setConnectionState("error");
  }, []);

  const reconnect = useCallback(() => {
    setConnectionState("connecting");
    // Force iframe reload by toggling the URL
    setDesktopUrl("");
    requestAnimationFrame(() => {
      setDesktopUrl(`/api/v1/instances/${instanceId}/desktop/`);
    });
  }, [instanceId]);

  return {
    connectionState,
    desktopUrl,
    setIframe,
    onLoad,
    onError,
    reconnect,
  };
}
