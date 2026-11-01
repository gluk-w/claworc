import { useEffect, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import AgentChatWorkspace from "@common/components/AgentChatWorkspace";
import { useQueryClient } from "@tanstack/react-query";
import { useInstance } from "@common/hooks/useInstances";
import { fetchInstance } from "@common/api/instances";
import type { ChatMessage } from "@common/types/chat";

/**
 * Asks the opener window for the chat history it already has. Skipped when
 * `skip` is set (agent switched inside the pop-out — the opener only answers
 * for the agent it opened, so asking would just wait out the timeout).
 */
function useHistoryFromOpener(instanceId: number, skip: boolean): ChatMessage[] | undefined {
  const askOpener = !!window.opener && !skip;
  const [history, setHistory] = useState<ChatMessage[] | undefined>(askOpener ? undefined : []);

  useEffect(() => {
    if (!askOpener) return;

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
  }, [instanceId, askOpener]);

  return history;
}

/** Inner component — only mounted once initial messages are resolved */
function ChatPopupInner({ instanceId, initialMessages }: { instanceId: number; initialMessages: ChatMessage[] }) {
  const navigate = useNavigate();
  const { data: instance, isLoading } = useInstance(instanceId);
  const displayName = instance?.display_name;

  // Show the agent's name in the window/tab title.
  useEffect(() => {
    if (!displayName) return;
    const prev = document.title;
    document.title = `${displayName} — Chat`;
    return () => {
      document.title = prev;
    };
  }, [displayName]);

  const qc = useQueryClient();
  const handleSwitchAgent = async (id: number) => {
    if (id === instanceId) return;
    // Prefetch so the pop-out goes straight to the new chat (no loading flash).
    await qc.ensureQueryData({ queryKey: ["instances", id], queryFn: () => fetchInstance(id) });
    navigate(`/instances/${id}/chat`, { replace: true, state: { switched: true } });
  };

  if (isLoading) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Loading...</div>;
  }

  if (!instance) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Agent not found.</div>;
  }

  if (instance.status !== "running") {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Agent must be running to use Chat.</div>;
  }

  return (
    <div className="h-screen flex flex-col">
      <AgentChatWorkspace
        instance={instance}
        initialMessages={initialMessages}
        switcherMode="always"
        onSwitchAgent={handleSwitchAgent}
        showNewWindow={false}
      />
    </div>
  );
}

function ChatPopupForInstance({ instanceId, switched }: { instanceId: number; switched: boolean }) {
  const initialMessages = useHistoryFromOpener(instanceId, switched);

  if (initialMessages === undefined) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Loading...</div>;
  }

  return <ChatPopupInner instanceId={instanceId} initialMessages={initialMessages} />;
}

export default function ChatPopupPage() {
  const { id } = useParams<{ id: string }>();
  const location = useLocation();
  const instanceId = Number(id);
  const switched = !!(location.state as { switched?: boolean } | null)?.switched;

  // Key on the agent so switching resets chat history, sockets and VNC.
  return <ChatPopupForInstance key={instanceId} instanceId={instanceId} switched={switched} />;
}
