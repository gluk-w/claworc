import { useParams } from "react-router-dom";
import VncPanel from "@/components/VncPanel";
import { useDesktop } from "@/hooks/useDesktop";
import { useInstance } from "@/hooks/useInstances";

export default function VncPopupPage() {
  const { id } = useParams<{ id: string }>();
  const instanceId = Number(id);
  const { data: instance, isLoading } = useInstance(instanceId);
  const desktopHook = useDesktop(instanceId, instance?.status === "running");

  if (isLoading) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Loading...</div>;
  }

  if (!instance) {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Instance not found.</div>;
  }

  if (instance.status !== "running") {
    return <div className="flex items-center justify-center h-screen bg-gray-900 text-gray-400">Instance must be running to view Browser.</div>;
  }

  return (
    <div className="h-screen">
      <VncPanel
        instanceId={instanceId}
        connectionState={desktopHook.connectionState}
        desktopUrl={desktopHook.desktopUrl}
        setIframe={desktopHook.setIframe}
        onLoad={desktopHook.onLoad}
        onError={desktopHook.onError}
        reconnect={desktopHook.reconnect}
      />
    </div>
  );
}
