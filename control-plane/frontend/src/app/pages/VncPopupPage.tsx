import { useParams } from "react-router-dom";
import VncPanel from "@common/components/VncPanel";
import { useDesktop } from "@common/hooks/useDesktop";
import { useInstance } from "@common/hooks/useInstances";

export default function VncPopupPage() {
  const { id } = useParams<{ id: string }>();
  const instanceId = Number(id);
  const { data: instance, isLoading } = useInstance(instanceId);
  const desktopHook = useDesktop(instanceId, instance?.status === "running");

  if (isLoading) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Loading...</div>;
  }

  if (!instance) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Agent not found.</div>;
  }

  if (instance.status !== "running") {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Agent must be running to view Browser.</div>;
  }

  return (
    <div className="h-screen relative">
      <VncPanel
        instanceId={instanceId}
        connectionState={desktopHook.connectionState}
        containerRef={desktopHook.containerRef}
        reconnect={desktopHook.reconnect}
        copyFromRemote={desktopHook.copyFromRemote}
        pasteToRemote={desktopHook.pasteToRemote}
      />
    </div>
  );
}
