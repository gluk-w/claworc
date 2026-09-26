import { useCallback, useEffect, useRef, useState } from "react";
import type { ChatFrame, ChatMessage, ConnectionState } from "@common/types/chat";

let msgCounter = 0;
function nextId(): string {
  return `msg-${Date.now()}-${++msgCounter}`;
}

const BACKOFF_INITIAL = 1000;
const BACKOFF_MAX = 30000;
const MAX_RETRIES = 5;

export function useChat(instanceId: number, enabled: boolean, initialMessages?: ChatMessage[]) {
  const [messages, setMessages] = useState<ChatMessage[]>(initialMessages ?? []);
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("disconnected");
  const [thinkingLabel, setThinkingLabel] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const retriesRef = useRef(0);
  const backoffRef = useRef(BACKOFF_INITIAL);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const stableTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const enabledRef = useRef(enabled);
  // Track the current streaming assistant message so we can update it in-place
  const streamingRef = useRef<{ messageId: string; msgId: string } | null>(null);
  // Track completed message IDs so stray snapshots arriving after `end` don't create duplicates
  const completedMessagesRef = useRef<Set<string>>(new Set());

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
      let frame: ChatFrame;
      try {
        frame = JSON.parse(event.data);
      } catch {
        return;
      }
      if (!frame || typeof frame !== "object") return;

      // Backend handshake frame
      if ("type" in frame && frame.type === "connected") {
        setConnectionState("connected");
        // Only reset retries after connection is stable for 5s
        // This prevents infinite reconnect loops when connections drop immediately
        if (stableTimerRef.current) clearTimeout(stableTimerRef.current);
        stableTimerRef.current = setTimeout(() => {
          retriesRef.current = 0;
          backoffRef.current = BACKOFF_INITIAL;
        }, 5000);
        // Only add message if last message isn't already "Connected to Agent"
        setMessages((prev) => {
          const last = prev[prev.length - 1];
          if (last?.role === "system" && last.content === "Connected to Agent") {
            return prev;
          }
          return [...prev, { id: nextId(), role: "system", content: "Connected to Agent", timestamp: Date.now() }];
        });
        return;
      }

      // Normalized shim events (forwarded verbatim by the backend)
      if (!("event" in frame)) return;

      switch (frame.event) {
        case "start":
          setThinkingLabel("Thinking...");
          break;

        case "assistant": {
          const messageId = frame.message_id;
          const text = frame.text;
          if (!messageId || typeof text !== "string") break;

          setThinkingLabel(null);

          // Skip stray snapshots for messages already finalized by `end`
          if (completedMessagesRef.current.has(messageId)) break;

          const current = streamingRef.current;
          if (current && current.messageId === messageId) {
            // Cumulative snapshot — replace the streaming message's content in-place
            setMessages((prev) =>
              prev.map((m) =>
                m.id === current.msgId ? { ...m, content: text } : m,
              ),
            );
          } else {
            // New message_id — finalize any previous streaming message and create a new one
            if (current) completedMessagesRef.current.add(current.messageId);
            const msgId = nextId();
            streamingRef.current = { messageId, msgId };
            setMessages((prev) => [
              ...prev,
              { id: msgId, role: "agent", content: text, timestamp: Date.now() },
            ]);
          }
          break;
        }

        case "tool":
          setThinkingLabel("Working...");
          break;

        case "error":
          addSystemMessage(`Error: ${frame.text ?? frame.code ?? "unknown"}`);
          break;

        case "end": {
          setThinkingLabel(null);
          const current = streamingRef.current;
          if (current) completedMessagesRef.current.add(current.messageId);
          streamingRef.current = null;
          break;
        }

        // Unknown event types MUST be ignored (forward compatibility)
        default:
          break;
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

  /** Send /stop to abort any in-flight agent run */
  const sendCommand = useCallback((cmd: string) => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
    wsRef.current.send(JSON.stringify({ type: "chat", role: "user", content: cmd }));
  }, []);

  const stopResponse = useCallback(() => {
    sendCommand("/stop");
    setThinkingLabel(null);
    streamingRef.current = null;
  }, [sendCommand]);

  /** Abort current run, reset session, and clear local history */
  const newChat = useCallback(() => {
    sendCommand("/stop");
    sendCommand("/new");
    setThinkingLabel(null);
    streamingRef.current = null;
    completedMessagesRef.current.clear();
    setMessages([]);
  }, [sendCommand]);

  const reconnect = useCallback(() => {
    retriesRef.current = 0;
    backoffRef.current = BACKOFF_INITIAL;
    connect();
  }, [connect]);

  return {
    messages,
    connectionState,
    thinkingLabel,
    sendMessage,
    clearMessages,
    stopResponse,
    newChat,
    reconnect,
  };
}
