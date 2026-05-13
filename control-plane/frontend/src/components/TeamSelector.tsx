import { useEffect, useRef, useState } from "react";
import { ChevronDown, Plus, Settings, Users } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useTeam } from "@/contexts/TeamContext";
import { useAuth } from "@/contexts/AuthContext";
import { fetchTeams } from "@/api/teams";

interface TeamSelectorProps {
  onCreateTeam?: () => void;
}

// TeamSelector renders the active-team dropdown shown on pages that scope
// data by team (Instances). Admins see a "+ Create a team" entry below the
// list; everyone else sees just the team list. The selection is persisted
// in localStorage by TeamContext.
export default function TeamSelector({ onCreateTeam }: TeamSelectorProps) {
  const { teams, activeTeam, setActiveTeamId } = useTeam();
  const { isAdmin } = useAuth();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

  // Pull instance counts from /teams; the auth-me payload doesn't include them.
  const { data: teamsWithCounts = [] } = useQuery({
    queryKey: ["teams"],
    queryFn: fetchTeams,
  });
  const instanceCountById = new Map(
    teamsWithCounts.map((t) => [t.id, t.instance_count ?? 0]),
  );

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  if (teams.length === 0 && !isAdmin) {
    return null;
  }

  return (
    <div ref={wrapRef} className="relative inline-block">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
      >
        <Users size={14} />
        <span className="max-w-[12rem] truncate">{activeTeam?.name ?? "Team"}</span>
        <ChevronDown size={14} className="text-gray-400" />
      </button>
      {open && (
        <div className="absolute left-0 mt-1 w-56 bg-white border border-gray-200 rounded-md shadow-lg z-20">
          <ul className="py-1 max-h-72 overflow-auto">
            {teams.map((t) => (
              <li key={t.id}>
                <button
                  type="button"
                  onClick={() => {
                    setActiveTeamId(t.id);
                    setOpen(false);
                  }}
                  className={`w-full flex items-center justify-between px-3 py-1.5 text-sm text-left hover:bg-gray-50 ${
                    t.id === activeTeam?.id ? "bg-gray-100 font-medium" : ""
                  }`}
                >
                  <span className="truncate">{t.name}</span>
                  <span className="text-xs text-gray-400">
                    {instanceCountById.get(t.id) ?? 0}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {isAdmin && (onCreateTeam || teams.length > 1) && (
            <div className="border-t border-gray-100 py-1">
              {onCreateTeam && (
                <button
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    onCreateTeam();
                  }}
                  className="w-full flex items-center gap-1.5 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
                >
                  <Plus size={14} />
                  Create a team
                </button>
              )}
              {teams.length > 1 && (
                <button
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    navigate("/teams");
                  }}
                  className="w-full flex items-center gap-1.5 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
                >
                  <Settings size={14} />
                  Manage
                </button>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
