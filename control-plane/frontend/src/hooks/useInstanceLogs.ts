import { useCallback, useEffect, useRef, useState } from "react";

export type LogType = "runtime" | "creation";

export function useInstanceLogs(
  id: number,
  enabled: boolean,
  logType: LogType = "runtime",
) {
  const [logs, setLogs] = useState<string[]>([]);
  const [isPaused, setIsPaused] = useState(false);
  const [isConnected, setIsConnected] = useState(false);
  const eventSourceRef = useRef<EventSource | null>(null);
  const pausedRef = useRef(false);

  useEffect(() => {
    pausedRef.current = isPaused;
  }, [isPaused]);

  useEffect(() => {
    if (!enabled) {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
        setIsConnected(false);
      }
      return;
    }

    const url =
      logType === "creation"
        ? `/api/v1/instances/${id}/creation-logs`
        : `/api/v1/instances/${id}/logs?tail=100&follow=true`;

    const es = new EventSource(url);
    eventSourceRef.current = es;

    es.onopen = () => setIsConnected(true);

    es.onmessage = (event) => {
      if (!pausedRef.current) {
        setLogs((prev) => [...prev, event.data as string]);
      }
    };

    es.onerror = () => {
      setIsConnected(false);
      es.close();
    };

    return () => {
      es.close();
      eventSourceRef.current = null;
      setIsConnected(false);
    };
  }, [id, enabled, logType]);

  // Clear logs when switching log types so streams don't mix
  useEffect(() => {
    setLogs([]);
  }, [logType]);

  const clearLogs = useCallback(() => setLogs([]), []);
  const togglePause = useCallback(() => setIsPaused((p) => !p), []);

  return { logs, clearLogs, isPaused, togglePause, isConnected };
}
