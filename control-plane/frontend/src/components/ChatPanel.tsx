import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  Send,
  Plus,
  Square,
  Wifi,
  WifiOff,
  RefreshCw,
  Loader2,
  Monitor,
  ToggleLeft,
  ToggleRight,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { ChatMessage, ConnectionState } from "@/types/chat";

interface ChatPanelProps {
  messages: ChatMessage[];
  connectionState: ConnectionState;
  thinkingLabel?: string | null;
  onSend: (content: string) => void;
  onStop: () => void;
  onNewChat: () => void;
  onReconnect: () => void;
  viewMode?: "chat-browser" | "chat-only";
  onViewModeChange?: (mode: "chat-browser" | "chat-only") => void;
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
        className={`max-w-[80%] rounded-lg px-3 py-2 text-sm ${
          isUser
            ? "bg-blue-600 text-white whitespace-pre-wrap"
            : "bg-gray-700 text-gray-200"
        }`}
      >
        {isUser ? (
          msg.content
        ) : (
          <div className="markdown-agent [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={{
                p: ({ children }) => <p className="mb-2">{children}</p>,
                pre: ({ children }) => (
                  <pre className="bg-gray-800 rounded p-2 my-2 overflow-x-auto text-xs">{children}</pre>
                ),
                code: ({ children, className }) => {
                  const isBlock = className?.startsWith("language-");
                  return isBlock ? (
                    <code className={className}>{children}</code>
                  ) : (
                    <code className="bg-gray-600 rounded px-1 py-0.5 text-xs">{children}</code>
                  );
                },
                a: ({ href, children }) => (
                  <a href={href} target="_blank" rel="noopener noreferrer" className="text-blue-400 underline hover:text-blue-300">
                    {children}
                  </a>
                ),
                ul: ({ children }) => <ul className="list-disc pl-4 mb-2">{children}</ul>,
                ol: ({ children }) => <ol className="list-decimal pl-4 mb-2">{children}</ol>,
                li: ({ children }) => <li className="mb-0.5">{children}</li>,
                strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
                blockquote: ({ children }) => (
                  <blockquote className="border-l-2 border-gray-500 pl-2 my-2 text-gray-400">{children}</blockquote>
                ),
                table: ({ children }) => (
                  <div className="overflow-x-auto my-2">
                    <table className="border-collapse text-xs">{children}</table>
                  </div>
                ),
                th: ({ children }) => <th className="border border-gray-600 px-2 py-1 bg-gray-800">{children}</th>,
                td: ({ children }) => <td className="border border-gray-600 px-2 py-1">{children}</td>,
              }}
            >
              {msg.content}
            </ReactMarkdown>
          </div>
        )}
      </div>
    </div>
  );
}

function TypingIndicator({ label }: { label: string }) {
  return (
    <div className="flex justify-start mb-2">
      <div className="bg-gray-700 rounded-lg px-3 py-2 flex items-center gap-2">
        <span className="flex gap-1">
          <span className="w-1.5 h-1.5 bg-gray-400 rounded-full animate-[pulse_1.4s_ease-in-out_infinite]" />
          <span className="w-1.5 h-1.5 bg-gray-400 rounded-full animate-[pulse_1.4s_ease-in-out_0.2s_infinite]" />
          <span className="w-1.5 h-1.5 bg-gray-400 rounded-full animate-[pulse_1.4s_ease-in-out_0.4s_infinite]" />
        </span>
        <span className="text-xs text-gray-400">{label}</span>
      </div>
    </div>
  );
}

export default function ChatPanel({
  messages,
  connectionState,
  thinkingLabel,
  onSend,
  onStop,
  onNewChat,
  onReconnect,
  viewMode,
  onViewModeChange,
}: ChatPanelProps) {
  const [input, setInput] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, thinkingLabel]);

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
          onClick={onNewChat}
          className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded"
          title="New chat"
        >
          <Plus size={14} /> New chat
        </button>
        {onViewModeChange && (
          <button
            onClick={() => onViewModeChange(viewMode === "chat-browser" ? "chat-only" : "chat-browser")}
            className={`flex items-center gap-1 px-1.5 py-1 text-xs rounded ${viewMode === "chat-browser" ? "text-blue-400" : "text-gray-400 hover:text-white"}`}
            title="Show/Hide browser"
          >
            {viewMode === "chat-browser" ? <ToggleRight size={14} /> : <ToggleLeft size={14} />} Browser
          </button>
        )}
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
        {thinkingLabel && connectionState === "connected" && <TypingIndicator label={thinkingLabel} />}
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
        {thinkingLabel && connectionState === "connected" ? (
          <button
            type="button"
            onClick={onStop}
            className="p-1.5 text-red-400 hover:text-red-300 rounded"
            title="Stop"
          >
            <Square size={16} fill="currentColor" />
          </button>
        ) : (
          <button
            type="submit"
            disabled={connectionState !== "connected" || !input.trim()}
            className="p-1.5 text-gray-400 hover:text-white rounded disabled:opacity-30"
            title="Send"
          >
            <Send size={16} />
          </button>
        )}
      </form>
    </div>
  );
}
