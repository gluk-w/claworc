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
  const enabledRef = useRef(enabled);

  useEffect(() => {
    enabledRef.current = enabled;
  }, [enabled]);

  const desktopUrl = `/api/v1/instances/${instanceId}/desktop/`;

  const setIframe = useCallback((el: HTMLIFrameElement | null) => {
    iframeRef.current = el;
  }, []);

  const onLoad = useCallback(() => {
    if (enabledRef.current) {
      setConnectionState("connected");
    }
  }, []);

  const onError = useCallback(() => {
    setConnectionState("error");
  }, []);

  const reconnect = useCallback(() => {
    setConnectionState("connecting");
    if (iframeRef.current) {
      iframeRef.current.src = desktopUrl;
    }
  }, [desktopUrl]);

  useEffect(() => {
    if (enabled) {
      setConnectionState("connecting");
    } else {
      setConnectionState("disconnected");
    }
  }, [enabled]);

  return {
    connectionState,
    desktopUrl,
    setIframe,
    onLoad,
    onError,
    reconnect,
  };
}
