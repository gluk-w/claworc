import { useCallback, useEffect, useRef, useState } from "react";
import type { ChatMessage, ConnectionState, GatewayFrame } from "@/types/chat";

let msgCounter = 0;
function nextId(): string {
  return `msg-${Date.now()}-${++msgCounter}`;
}

const BACKOFF_INITIAL = 1000;
const BACKOFF_MAX = 30000;
const MAX_RETRIES = 5;

export function useChat(instanceId: number, enabled: boolean) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");
  const wsRef = useRef<WebSocket | null>(null);
  const retriesRef = useRef(0);
  const backoffRef = useRef(BACKOFF_INITIAL);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const stableTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const enabledRef = useRef(enabled);

  useEffect(() => {
    enabledRef.current = enabled;
  }, [enabled]);

  const clearReconnectTimer = useCallback(() => {
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const addSystemMessage = useCallback((content: string) => {
    setMessages((prev) => [
      ...prev,
      { id: nextId(), role: "system", content, timestamp: Date.now() },
    ]);
  }, []);

  const disconnect = useCallback(() => {
    clearReconnectTimer();
    if (stableTimerRef.current) {
      clearTimeout(stableTimerRef.current);
      stableTimerRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    setConnectionState("disconnected");
  }, [clearReconnectTimer]);

  const connect = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    clearReconnectTimer();

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${window.location.host}/api/v1/instances/${instanceId}/chat`;

    setConnectionState("connecting");

    const ws = new WebSocket(url);
    wsRef.current = ws;

    ws.onopen = () => {
      // Wait for {type: "connected"} from backend before marking as connected
    };

    ws.onmessage = (event) => {
      let frame: GatewayFrame;
      try {
        frame = JSON.parse(event.data);
      } catch {
        return;
      }

      switch (frame.type) {
        case "connected":
          setConnectionState("connected");
          // Only reset retries after connection is stable for 5s
          // This prevents infinite reconnect loops when connections drop immediately
          if (stableTimerRef.current) clearTimeout(stableTimerRef.current);
          stableTimerRef.current = setTimeout(() => {
            retriesRef.current = 0;
            backoffRef.current = BACKOFF_INITIAL;
          }, 5000);
          // Only add message if last message isn't already "Connected to Gateway"
          setMessages((prev) => {
            const last = prev[prev.length - 1];
            if (last?.role === "system" && last.content === "Connected to Gateway") {
              return prev;
            }
            return [...prev, { id: nextId(), role: "system", content: "Connected to Gateway", timestamp: Date.now() }];
          });
          break;

        case "chat":
          setMessages((prev) => [
            ...prev,
            {
              id: nextId(),
              role: frame.role,
              content: frame.content,
              timestamp: Date.now(),
            },
          ]);
          break;

        case "agent": {
          const eventName = frame.event;
          if (eventName === "thinking") {
            addSystemMessage("Agent is thinking...");
          } else if (eventName === "tool_use") {
            const toolData = frame.data as { name?: string } | undefined;
            addSystemMessage(
              `Agent using tool: ${toolData?.name ?? "unknown"}`,
            );
          }
          break;
        }

        case "error":
          addSystemMessage(`Error: ${frame.message}`);
          break;

        // Raw gateway event frames (forwarded as-is from the gateway)
        case "event": {
          const ev = frame.event;
          const payload = frame.payload as Record<string, unknown> | undefined;
          // Skip heartbeat ticks and presence events
          if (ev === "tick" || ev === "presence") break;
          // Try to extract chat message content from event payload
          const content = (payload?.message ?? payload?.content ?? payload?.text) as string | undefined;
          if (content) {
            const role = (payload?.role === "user") ? "user" as const : "agent" as const;
            setMessages((prev) => [
              ...prev,
              { id: nextId(), role, content, timestamp: Date.now() },
            ]);
          } else {
            // Log unknown events for debugging
            console.log("[chat] gateway event:", ev, payload);
          }
          break;
        }

        // Raw gateway response frames (ack for chat.send etc.)
        case "res": {
          if (!frame.ok && frame.error) {
            addSystemMessage(`Error: ${frame.error.message ?? frame.error.code ?? "unknown"}`);
          }
          break;
        }
      }
    };

    ws.onclose = (event) => {
      wsRef.current = null;

      // Application error codes (4xxx) are terminal
      if (event.code >= 4000 && event.code < 5000) {
        setConnectionState("error");
        addSystemMessage(
          event.reason || `Connection closed (code ${event.code})`,
        );
        return;
      }

      setConnectionState("disconnected");

      // Auto-reconnect if still enabled
      if (enabledRef.current && retriesRef.current < MAX_RETRIES) {
        const delay = backoffRef.current;
        retriesRef.current += 1;
        backoffRef.current = Math.min(delay * 2, BACKOFF_MAX);
        reconnectTimerRef.current = setTimeout(() => {
          if (enabledRef.current) {
            connect();
          }
        }, delay);
      }
    };

    ws.onerror = () => {
      // onclose will fire after this
    };
  }, [instanceId, clearReconnectTimer, addSystemMessage]);

  // Connect/disconnect based on enabled flag
  useEffect(() => {
    if (enabled) {
      retriesRef.current = 0;
      backoffRef.current = BACKOFF_INITIAL;
      connect();
    } else {
      disconnect();
    }
    return () => {
      disconnect();
    };
  }, [enabled, connect, disconnect]);

  const sendMessage = useCallback(
    (content: string) => {
      if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;

      // Optimistic UI update
      setMessages((prev) => [
        ...prev,
        { id: nextId(), role: "user", content, timestamp: Date.now() },
      ]);

      wsRef.current.send(
        JSON.stringify({ type: "chat", role: "user", content }),
      );
    },
    [],
  );

  const clearMessages = useCallback(() => setMessages([]), []);

  const reconnect = useCallback(() => {
    retriesRef.current = 0;
    backoffRef.current = BACKOFF_INITIAL;
    connect();
  }, [connect]);

  return {
    messages,
    connectionState,
    sendMessage,
    clearMessages,
    reconnect,
  };
}
