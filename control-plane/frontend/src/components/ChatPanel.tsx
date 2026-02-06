import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  Send,
  Trash2,
  Wifi,
  WifiOff,
  RefreshCw,
  Loader2,
} from "lucide-react";
import type { ChatMessage, ConnectionState } from "@/types/chat";

interface ChatPanelProps {
  messages: ChatMessage[];
  connectionState: ConnectionState;
  onSend: (content: string) => void;
  onClear: () => void;
  onReconnect: () => void;
}

function ConnectionIndicator({ state }: { state: ConnectionState }) {
  switch (state) {
    case "connected":
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <Wifi size={12} className="text-green-400" /> Connected
        </span>
      );
    case "connecting":
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <Loader2 size={12} className="text-yellow-400 animate-spin" />{" "}
          Connecting
        </span>
      );
    case "error":
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <WifiOff size={12} className="text-red-400" /> Error
        </span>
      );
    default:
      return (
        <span className="flex items-center gap-1 text-xs text-gray-400">
          <WifiOff size={12} className="text-red-400" /> Disconnected
        </span>
      );
  }
}

function MessageBubble({ msg }: { msg: ChatMessage }) {
  if (msg.role === "system") {
    return (
      <div className="text-center text-xs text-gray-500 py-1">
        {msg.content}
      </div>
    );
  }

  const isUser = msg.role === "user";

  return (
    <div className={`flex ${isUser ? "justify-end" : "justify-start"} mb-2`}>
      <div
        className={`max-w-[80%] rounded-lg px-3 py-2 text-sm whitespace-pre-wrap ${
          isUser
            ? "bg-blue-600 text-white"
            : "bg-gray-700 text-gray-200"
        }`}
      >
        {msg.content}
      </div>
    </div>
  );
}

export default function ChatPanel({
  messages,
  connectionState,
  onSend,
  onClear,
  onReconnect,
}: ChatPanelProps) {
  const [input, setInput] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = input.trim();
    if (!trimmed || connectionState !== "connected") return;
    onSend(trimmed);
    setInput("");
  };

  return (
    <div className="flex flex-col h-full">
      {/* Header bar — matches LogViewer */}
      <div className="flex items-center gap-2 px-3 py-2 bg-gray-800 border-b border-gray-700">
        <button
          onClick={onClear}
          className="p-1 text-gray-400 hover:text-white rounded"
          title="Clear chat"
        >
          <Trash2 size={14} />
        </button>
        {(connectionState === "disconnected" || connectionState === "error") && (
          <button
            onClick={onReconnect}
            className="p-1 text-gray-400 hover:text-white rounded"
            title="Reconnect"
          >
            <RefreshCw size={14} />
          </button>
        )}
        <div className="flex-1" />
        <ConnectionIndicator state={connectionState} />
      </div>

      {/* Messages area */}
      <div className="flex-1 overflow-auto bg-gray-900 p-3 min-h-[300px]">
        {messages.length === 0 ? (
          <div className="text-gray-500 text-sm">
            {connectionState === "connected"
              ? "Send a message to start chatting..."
              : "Connecting to gateway..."}
          </div>
        ) : (
          messages.map((msg) => <MessageBubble key={msg.id} msg={msg} />)
        )}
        <div ref={bottomRef} />
      </div>

      {/* Input bar */}
      <form
        onSubmit={handleSubmit}
        className="flex items-center gap-2 px-3 py-2 bg-gray-800 border-t border-gray-700"
      >
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder={
            connectionState === "connected"
              ? "Type a message..."
              : "Waiting for connection..."
          }
          disabled={connectionState !== "connected"}
          className="flex-1 bg-gray-700 text-gray-200 text-sm rounded px-3 py-1.5 outline-none placeholder-gray-500 disabled:opacity-50"
        />
        <button
          type="submit"
          disabled={connectionState !== "connected" || !input.trim()}
          className="p-1.5 text-gray-400 hover:text-white rounded disabled:opacity-30"
          title="Send"
        >
          <Send size={16} />
        </button>
      </form>
    </div>
  );
}
