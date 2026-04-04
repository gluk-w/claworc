import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import ChatPanel from "@/components/ChatPanel";
import VncPanel from "@/components/VncPanel";
import { useChat } from "@/hooks/useChat";
import { useDesktop } from "@/hooks/useDesktop";
import { useInstance } from "@/hooks/useInstances";
import type { ChatMessage } from "@/types/chat";

function useHistoryFromOpener(instanceId: number): ChatMessage[] | undefined {
  const [history, setHistory] = useState<ChatMessage[] | undefined>(
    window.opener ? undefined : [],
  );

  useEffect(() => {
    if (!window.opener) return;

    const handler = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) return;
      if (e.data?.type === "chat-history") {
        setHistory(e.data.messages ?? []);
      }
    };
    window.addEventListener("message", handler);
    window.opener.postMessage({ type: "chat-history-request", instanceId }, window.location.origin);

    const timeout = setTimeout(() => setHistory((prev) => prev ?? []), 2000);

    return () => {
      window.removeEventListener("message", handler);
      clearTimeout(timeout);
    };
  }, [instanceId]);

  return history;
}

/** Inner component — only mounted once initial messages are resolved */
function ChatPopupInner({ instanceId, initialMessages }: { instanceId: number; initialMessages: ChatMessage[] }) {
  const { data: instance, isLoading } = useInstance(instanceId);
  const [chatViewMode, setChatViewMode] = useState<"chat-browser" | "chat-only">("chat-browser");
  const chatHook = useChat(instanceId, instance?.status === "running", initialMessages);
  const desktopHook = useDesktop(instanceId, chatViewMode === "chat-browser" && instance?.status === "running");

  if (isLoading) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Loading...</div>;
  }

  if (!instance) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Instance not found.</div>;
  }

  if (instance.status !== "running") {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Instance must be running to use Chat.</div>;
  }

  return (
    <div className="h-screen flex">
      <div className={chatViewMode === "chat-browser" ? "w-[400px] flex-shrink-0 border-r border-gray-700 relative" : "flex-1 relative"}>
        <ChatPanel
          messages={chatHook.messages}
          connectionState={chatHook.connectionState}
          thinkingLabel={chatHook.thinkingLabel}
          onSend={chatHook.sendMessage}
          onStop={chatHook.stopResponse}
          onNewChat={chatHook.newChat}
          onReconnect={chatHook.reconnect}
          viewMode={chatViewMode}
          onViewModeChange={setChatViewMode}
        />
      </div>
      {chatViewMode === "chat-browser" && (
        <div className="flex-1 min-w-0 relative">
          <VncPanel
            instanceId={instanceId}
            connectionState={desktopHook.connectionState}
            containerRef={desktopHook.containerRef}
            reconnect={desktopHook.reconnect}
            copyFromRemote={desktopHook.copyFromRemote}
            pasteToRemote={desktopHook.pasteToRemote}
            showNewWindow={false}
            showFullscreen={false}
          />
        </div>
      )}
    </div>
  );
}

export default function ChatPopupPage() {
  const { id } = useParams<{ id: string }>();
  const instanceId = Number(id);
  const initialMessages = useHistoryFromOpener(instanceId);

  if (initialMessages === undefined) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Loading...</div>;
  }

  return <ChatPopupInner instanceId={instanceId} initialMessages={initialMessages} />;
}
