import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, Maximize, Minimize } from "lucide-react";
import ChatPanel from "@common/components/ChatPanel";
import VncPanel from "@common/components/VncPanel";
import { useChat } from "@common/hooks/useChat";
import { useDesktop } from "@common/hooks/useDesktop";
import { useChatViewMode } from "@common/hooks/useChatViewMode";
import { useInstances } from "@common/hooks/useInstances";
import { useTeam } from "@common/contexts/TeamContext";
import { fetchTeams } from "@common/api/teams";
import { stopBrowser } from "@common/api/browser";
import type { Instance } from "@common/types/instance";
import type { ChatMessage } from "@common/types/chat";

/** Tracks whether `ref.current` is the document's fullscreen element. */
function useIsFullscreen(ref: RefObject<HTMLElement | null>): boolean {
  const [isFullscreen, setIsFullscreen] = useState(
    () => !!ref.current && document.fullscreenElement === ref.current,
  );
  useEffect(() => {
    const update = () => setIsFullscreen(!!ref.current && document.fullscreenElement === ref.current);
    update();
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, [ref]);
  return isFullscreen;
}

interface AgentSwitcherProps {
  currentId: number;
  onSwitch: (id: number) => void;
}

/**
 * Compact dark dropdown listing every agent the user can access, grouped by
 * team. The selected option is the current agent, so it doubles as the
 * agent-name title in full screen / pop-out mode.
 */
function AgentSwitcher({ currentId, onSwitch }: AgentSwitcherProps) {
  const { data: instances = [] } = useInstances();
  const { teams: memberships } = useTeam();
  // Same query key as TeamSelector so the list is shared from cache.
  const { data: teams = [] } = useQuery({ queryKey: ["teams"], queryFn: fetchTeams });

  const groups = useMemo(() => {
    const teamName = new Map<number, string>();
    memberships.forEach((t) => teamName.set(t.id, t.name));
    teams.forEach((t) => teamName.set(t.id, t.name));

    const byTeam = new Map<number, Instance[]>();
    for (const inst of instances) {
      const list = byTeam.get(inst.team_id) ?? [];
      list.push(inst);
      byTeam.set(inst.team_id, list);
    }
    return [...byTeam.entries()]
      .map(([teamId, list]) => ({
        teamId,
        label: teamName.get(teamId) ?? `Team #${teamId}`,
        instances: [...list].sort((a, b) => a.display_name.localeCompare(b.display_name)),
      }))
      .sort((a, b) => a.label.localeCompare(b.label));
  }, [instances, teams, memberships]);

  return (
    <select
      aria-label="Switch agent"
      title="Switch agent"
      value={currentId}
      onChange={(e) => onSwitch(Number(e.target.value))}
      className="max-w-[320px] truncate px-2 py-1 bg-gray-900 border border-gray-700 rounded-md text-xs font-medium text-gray-200 focus:outline-none focus:ring-2 focus:ring-blue-500"
    >
      {/* Ensure the current agent is selectable before the list loads. */}
      {!instances.some((i) => i.id === currentId) && <option value={currentId}>Loading...</option>}
      {groups.map((g) => (
        <optgroup key={g.teamId} label={g.label}>
          {g.instances.map((inst) => {
            const running = inst.status === "running";
            return (
              <option key={inst.id} value={inst.id} disabled={!running && inst.id !== currentId}>
                {inst.display_name}
                {running ? "" : ` (${inst.status})`}
              </option>
            );
          })}
        </optgroup>
      ))}
    </select>
  );
}

interface AgentChatWorkspaceProps {
  /** Must be running. Render with `key={instance.id}` so switching agents remounts cleanly. */
  instance: Instance;
  initialMessages?: ChatMessage[];
  /** "fullscreen": show the agent switcher only while in full screen. "always": always show it. */
  switcherMode: "fullscreen" | "always";
  onSwitchAgent: (id: number) => void;
  showNewWindow?: boolean;
  /**
   * Element to put in full screen. Defaults to the workspace root. Pass a
   * parent element that survives an agent switch so full screen stays on.
   */
  fullscreenRef?: RefObject<HTMLDivElement | null>;
}

export default function AgentChatWorkspace({
  instance,
  initialMessages,
  switcherMode,
  onSwitchAgent,
  showNewWindow = true,
  fullscreenRef,
}: AgentChatWorkspaceProps) {
  const instanceId = instance.id;
  const rootRef = useRef<HTMLDivElement>(null);
  const targetRef = fullscreenRef ?? rootRef;
  const isFullscreen = useIsFullscreen(targetRef);

  const [chatViewMode, setChatViewMode] = useChatViewMode(instanceId, instance.browser_active);
  const chatHook = useChat(instanceId, true, initialMessages, {
    canStop: instance.agent_capabilities?.chat_abort,
    canReset: instance.agent_capabilities?.session_reset,
  });
  const desktopHook = useDesktop(instanceId, chatViewMode === "chat-browser");

  // When the user hides the browser pane, also stop the on-demand browser pod
  // so we don't burn resources on something nobody can see. Re-enabling the
  // pane lets DesktopProxy/EnsureSession spin a fresh one back up automatically
  // — no explicit start call needed here.
  const lastViewModeRef = useRef(chatViewMode);
  useEffect(() => {
    if (instance.is_legacy_embedded) return;
    const prev = lastViewModeRef.current;
    lastViewModeRef.current = chatViewMode;
    if (prev === "chat-browser" && chatViewMode === "chat-only") {
      stopBrowser(instanceId).catch(() => {
        // Best-effort: a 404/stopped browser is fine; log nothing to avoid
        // toast spam on a UI-only action.
      });
    }
  }, [chatViewMode, instance.is_legacy_embedded, instanceId]);

  const openNewWindow = () => {
    const popup = window.open(`/instances/${instanceId}/chat`, "_blank");
    if (!popup) return;
    const handler = (e: MessageEvent) => {
      if (e.source === popup && e.data?.type === "chat-history-request" && e.data?.instanceId === instanceId) {
        popup.postMessage({ type: "chat-history", messages: chatHook.messages }, window.location.origin);
        window.removeEventListener("message", handler);
      }
    };
    window.addEventListener("message", handler);
  };

  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      document.exitFullscreen();
    } else {
      targetRef.current?.requestFullscreen();
    }
  };

  const showSwitcher = switcherMode === "always" || isFullscreen;

  return (
    <div ref={rootRef} className="flex flex-col flex-1 h-full min-h-0 bg-gray-900">
      {/* Agent switcher (left) / New Window + Full Screen (right) */}
      <div className="flex items-center justify-between gap-2 px-3 py-1 bg-gray-800 border-b border-gray-700">
        <div className="min-w-0">
          {showSwitcher && <AgentSwitcher currentId={instanceId} onSwitch={onSwitchAgent} />}
        </div>
        <div className="flex items-center gap-2 flex-shrink-0">
          {showNewWindow && (
            <button
              onClick={openNewWindow}
              className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded"
              title="Open in new window"
            >
              <ExternalLink size={14} /> New Window
            </button>
          )}
          <button
            onClick={toggleFullscreen}
            className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded"
            title="Toggle fullscreen"
          >
            {isFullscreen ? <Minimize size={14} /> : <Maximize size={14} />}
            {isFullscreen ? " Exit Full Screen" : " Full Screen"}
          </button>
        </div>
      </div>
      <div className="flex flex-1 min-h-0">
        <div className={chatViewMode === "chat-browser" ? "w-[400px] flex-shrink-0 border-r border-gray-700 relative" : "flex-1 relative"}>
          <ChatPanel
            messages={chatHook.messages}
            connectionState={chatHook.connectionState}
            thinkingLabel={chatHook.thinkingLabel}
            onSend={chatHook.sendMessage}
            onStop={chatHook.stopResponse}
            onNewChat={chatHook.newChat}
            canStop={chatHook.canStop}
            canNewChat={chatHook.canReset}
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
    </div>
  );
}
