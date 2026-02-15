import { useParams } from "react-router-dom";
import VncPanel from "@/components/VncPanel";
import { useVnc } from "@/hooks/useVnc";
import { useInstance } from "@/hooks/useInstances";

export default function VncPopupPage() {
  const { id } = useParams<{ id: string }>();
  const instanceId = Number(id);
  const { data: instance, isLoading } = useInstance(instanceId);
  const vncHook = useVnc(instanceId, instance?.status === "running");

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
        connectionState={vncHook.connectionState}
        setContainer={vncHook.setContainer}
        reconnect={vncHook.reconnect}
        copyFromVnc={vncHook.copyFromVnc}
        pasteToVnc={vncHook.pasteToVnc}
      />
    </div>
  );
}
