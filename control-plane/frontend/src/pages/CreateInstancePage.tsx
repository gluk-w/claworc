import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { errorToast } from "@/utils/toast";
import InstanceForm from "@/components/InstanceForm";
import { useCreateInstance } from "@/hooks/useInstances";
import { useTeam } from "@/contexts/TeamContext";
import { useAuth } from "@/contexts/AuthContext";


export default function CreateInstancePage() {
  const navigate = useNavigate();
  const createMutation = useCreateInstance();
  const { teams, activeTeamId } = useTeam();
  const { isAdmin } = useAuth();

  const allowedTeams = useMemo(
    () => (isAdmin ? teams : teams.filter((t) => t.role === "manager")),
    [teams, isAdmin],
  );

  const [teamId, setTeamId] = useState<number | null>(() => {
    if (activeTeamId && allowedTeams.some((t) => t.id === activeTeamId)) {
      return activeTeamId;
    }
    return allowedTeams[0]?.id ?? null;
  });

  // Keep selection valid as the allowed list resolves.
  useEffect(() => {
    if (allowedTeams.length === 0) return;
    if (!teamId || !allowedTeams.some((t) => t.id === teamId)) {
      setTeamId(allowedTeams[0].id);
    }
  }, [allowedTeams, teamId]);

  if (allowedTeams.length === 0) {
    return (
      <div>
        <button
          onClick={() => navigate("/")}
          className="inline-flex items-center gap-1 text-sm text-gray-600 hover:text-gray-900 mb-4"
        >
          <ArrowLeft size={16} />
          Back to Dashboard
        </button>
        <div className="max-w-2xl bg-white rounded-lg border border-gray-200 p-6 text-sm text-gray-700">
          You don't have permission to create instances. Ask an admin to make
          you a manager of a team.
        </div>
      </div>
    );
  }

  return (
    <div>
      <button
        onClick={() => navigate("/")}
        className="inline-flex items-center gap-1 text-sm text-gray-600 hover:text-gray-900 mb-4"
      >
        <ArrowLeft size={16} />
        Back to Dashboard
      </button>

      <h1 className="text-xl font-semibold text-gray-900 mb-6">
        Create Instance
      </h1>

      <div className="max-w-2xl">
        <InstanceForm
          teams={allowedTeams}
          teamId={teamId}
          onTeamIdChange={setTeamId}
          onSubmit={(payload) =>
            createMutation.mutate({ ...payload, team_id: teamId ?? undefined }, {
              onSuccess: () => {
                navigate("/");
              },
              onError: (error: any) => {
                if (error.response?.status === 409) {
                  errorToast("Failed to create instance", "An instance with the same name already exists");
                } else {
                  errorToast("Failed to create instance", error);
                }
              },
            })
          }
          onCancel={() => navigate("/")}
          loading={createMutation.isPending}
        />
      </div>
    </div>
  );
}
