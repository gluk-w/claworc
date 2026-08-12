export type ConnectionState = "disconnected" | "connecting" | "connected" | "error";

export interface ChatMessage {
  id: string;
  role: "user" | "agent" | "system";
  content: string;
  timestamp: number;
}

/** Handshake frame sent by the backend chat proxy right after the WebSocket opens */
export interface ConnectedFrame {
  type: "connected";
}

/**
 * Normalized agent shim chat events (see docs/shim.md, contract v1),
 * forwarded verbatim by the backend after the "connected" handshake.
 */
export interface ShimStartEvent {
  v: number;
  event: "start";
  session?: string;
  turn?: string;
}

export interface ShimAssistantEvent {
  v: number;
  event: "assistant";
  turn?: string;
  message_id: string;
  /** CUMULATIVE snapshot of the full message text so far (not a delta) */
  text: string;
}

export interface ShimToolEvent {
  v: number;
  event: "tool";
  turn?: string;
  name?: string;
  phase?: "start" | "result" | string;
  detail?: Record<string, unknown>;
}

export interface ShimErrorEvent {
  v: number;
  event: "error";
  turn?: string;
  code?: string;
  text?: string;
  fatal?: boolean;
}

export interface ShimEndEvent {
  v: number;
  event: "end";
  turn?: string;
  stop_reason?: "complete" | "aborted" | "error";
  /** Final text of the last assistant message */
  text?: string;
}

export type ShimChatEvent =
  | ShimStartEvent
  | ShimAssistantEvent
  | ShimToolEvent
  | ShimErrorEvent
  | ShimEndEvent;

/** Frames received over the instance chat WebSocket */
export type ChatFrame = ConnectedFrame | ShimChatEvent;
