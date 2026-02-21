import { useState, useEffect } from "react";
import { Shield } from "lucide-react";
import { useAllowedSourceIPs, useUpdateAllowedSourceIPs } from "@/hooks/useSSH";

interface SSHIPRestrictProps {
  instanceId: number;
}

export default function SSHIPRestrict({ instanceId }: SSHIPRestrictProps) {
  const { data, isLoading } = useAllowedSourceIPs(instanceId);
  const updateMutation = useUpdateAllowedSourceIPs(instanceId);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");

  useEffect(() => {
    if (data) {
      setDraft(data.allowed_ips);
    }
  }, [data?.allowed_ips]);

  const handleSave = () => {
    updateMutation.mutate(draft, {
      onSuccess: () => setEditing(false),
    });
  };

  const handleCancel = () => {
    setDraft(data?.allowed_ips ?? "");
    setEditing(false);
  };

  if (isLoading) {
    return null;
  }

  const hasRestrictions = !!data?.allowed_ips;

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <Shield size={16} className="text-gray-500" />
          <h3 className="text-sm font-medium text-gray-900">
            Source IP Restrictions
          </h3>
          {hasRestrictions ? (
            <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-green-100 text-green-800">
              Active
            </span>
          ) : (
            <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-600">
              Allow All
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={() => {
            if (editing) {
              handleCancel();
            } else {
              setEditing(true);
            }
          }}
          className="text-xs text-blue-600 hover:text-blue-800"
        >
          {editing ? "Cancel" : "Edit"}
        </button>
      </div>

      <p className="text-xs text-gray-500 mb-3">
        Restrict SSH connections to specific IP addresses or CIDR ranges. Leave
        empty to allow connections from any IP. Separate multiple entries with
        commas.
      </p>

      {editing ? (
        <div className="space-y-3">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="e.g. 10.0.0.1, 192.168.1.0/24, 2001:db8::/32"
            rows={3}
            className="w-full px-3 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500 font-mono"
          />
          <div className="flex justify-end gap-2">
            <button
              onClick={handleCancel}
              className="px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              onClick={handleSave}
              disabled={updateMutation.isPending}
              className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {updateMutation.isPending ? "Saving..." : "Save"}
            </button>
          </div>
        </div>
      ) : (
        <div className="text-sm text-gray-700 font-mono bg-gray-50 rounded px-3 py-2 min-h-[2rem]">
          {hasRestrictions
            ? data.allowed_ips.split(",").map((ip, i) => (
                <span key={i} className="inline-block mr-2">
                  {ip.trim()}
                  {i < data.allowed_ips.split(",").length - 1 && ","}
                </span>
              ))
            : <span className="text-gray-400 italic">No restrictions (all IPs allowed)</span>}
        </div>
      )}
    </div>
  );
}
