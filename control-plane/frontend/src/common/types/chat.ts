export type ConnectionState = "disconnected" | "connecting" | "connected" | "error";

export interface ChatMessage {
  id: string;
  role: "user" | "agent" | "system";
  content: string;
  timestamp: number;
}

/** Frames received from the Gateway (via backend proxy) */
export interface GatewayConnectedFrame {
  type: "connected";
}

export interface GatewayChatFrame {
  type: "chat";
  role: "agent" | "user";
  content: string;
}

export interface GatewayAgentFrame {
  type: "agent";
  event: string;
  data?: unknown;
}

export interface GatewayErrorFrame {
  type: "error";
  message: string;
}

/** Raw gateway event frame (forwarded as-is from the gateway) */
export interface GatewayEventFrame {
  type: "event";
  event: string;
  payload?: Record<string, unknown>;
  seq?: number;
}

/** Raw gateway response frame (ack for chat.send etc.) */
export interface GatewayResponseFrame {
  type: "res";
  id: string;
  ok: boolean;
  payload?: Record<string, unknown>;
  error?: { code?: string; message?: string };
}

export type GatewayFrame =
  | GatewayConnectedFrame
  | GatewayChatFrame
  | GatewayAgentFrame
  | GatewayErrorFrame
  | GatewayEventFrame
  | GatewayResponseFrame;
