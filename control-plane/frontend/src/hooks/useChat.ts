import { useCallback, useEffect, useRef, useState } from "react";
import type { ChatMessage, ConnectionState, GatewayFrame } from "@/types/chat";

let msgCounter = 0;
function nextId(): string {
  return `msg-${Date.now()}-${++msgCounter}`;
}

const BACKOFF_INITIAL = 1000;
const BACKOFF_MAX = 30000;
const MAX_RETRIES = 5;

/** Extract text from a gateway chat message content field (array of blocks or string). */
function extractText(content: unknown): string | undefined {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    const parts: string[] = [];
    for (const block of content) {
      if (typeof block === "string") parts.push(block);
      else if (block && typeof block === "object" && typeof (block as any).text === "string") {
        parts.push((block as any).text);
      }
    }
    return parts.length > 0 ? parts.join("") : undefined;
  }
  return undefined;
}

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
  // Track the current streaming run so we can update the message in-place
  const streamingRunRef = useRef<{ runId: string; msgId: string } | null>(null);
  // Track completed run IDs so chat snapshots arriving after lifecycle end don't create duplicates
  const completedRunsRef = useRef<Set<string>>(new Set());

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
            setThinkingLabel("Thinking...");
          } else if (eventName === "tool_use") {
            setThinkingLabel("Working...");
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
          if (!payload) break;

          // Skip heartbeat ticks, presence, and health events
          if (ev === "tick" || ev === "presence" || ev === "health") break;

          if (ev === "agent") {
            const stream = payload.stream as string | undefined;
            const data = payload.data as Record<string, unknown> | undefined;
            const runId = payload.runId as string | undefined;

            if (stream === "assistant" && data && runId) {
              const text = data.text as string | undefined;
              if (!text) break;

              setThinkingLabel(null);

              const current = streamingRunRef.current;
              if (current && current.runId === runId) {
                // Update existing streaming message in-place
                setMessages((prev) =>
                  prev.map((m) =>
                    m.id === current.msgId ? { ...m, content: text } : m,
                  ),
                );
              } else {
                // New run — create a new agent message
                const msgId = nextId();
                streamingRunRef.current = { runId, msgId };
                setMessages((prev) => [
                  ...prev,
                  { id: msgId, role: "agent", content: text, timestamp: Date.now() },
                ]);
              }
            } else if (stream === "lifecycle") {
              const phase = (data as any)?.phase as string | undefined;
              if (phase === "start") {
                setThinkingLabel("Thinking...");
              } else if (phase === "end") {
                setThinkingLabel(null);
                const cur = streamingRunRef.current;
                if (cur) completedRunsRef.current.add(cur.runId);
                streamingRunRef.current = null;
              }
            }
            break;
          }

          if (ev === "chat") {
            // Chat events are periodic snapshots — use them as fallback
            // if we missed the agent stream events
            const msg = payload.message as Record<string, unknown> | undefined;
            if (!msg) break;
            const runId = payload.runId as string | undefined;
            const text = extractText(msg.content);
            if (!text) break;
            const role = msg.role === "user" ? "user" as const : "agent" as const;

            // Skip if this run was already completed via agent stream events
            if (runId && completedRunsRef.current.has(runId)) break;

            const current = streamingRunRef.current;
            if (current && runId && current.runId === runId) {
              // Already tracking this run via agent events — update with snapshot
              setMessages((prev) =>
                prev.map((m) =>
                  m.id === current.msgId ? { ...m, content: text } : m,
                ),
              );
            } else if (!current || (runId && current.runId !== runId)) {
              // Not tracking — create a new message
              setThinkingLabel(null);
              const msgId = nextId();
              if (runId) {
                streamingRunRef.current = { runId, msgId };
              }
              setMessages((prev) => [
                ...prev,
                { id: msgId, role, content: text, timestamp: Date.now() },
              ]);
            }
            break;
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

  /** Send /stop to abort any in-flight agent run */
  const sendCommand = useCallback((cmd: string) => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
    wsRef.current.send(JSON.stringify({ type: "chat", role: "user", content: cmd }));
  }, []);

  const stopResponse = useCallback(() => {
    sendCommand("/stop");
    setThinkingLabel(null);
    streamingRunRef.current = null;
  }, [sendCommand]);

  /** Abort current run, reset session, and clear local history */
  const newChat = useCallback(() => {
    sendCommand("/stop");
    sendCommand("/new");
    setThinkingLabel(null);
    streamingRunRef.current = null;
    completedRunsRef.current.clear();
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
