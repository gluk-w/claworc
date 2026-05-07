import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useAuth } from "@/contexts/AuthContext";
import type { UserTeamMembership } from "@/types/auth";

const STORAGE_KEY = "claworc.activeTeamId";

interface TeamContextValue {
  teams: UserTeamMembership[];
  activeTeam: UserTeamMembership | null;
  activeTeamId: number | null;
  setActiveTeamId: (id: number | null) => void;
  isManager: (teamId?: number) => boolean;
}

const TeamContext = createContext<TeamContextValue | null>(null);

function readStoredTeamId(): number | null {
  try {
    const v = window.localStorage.getItem(STORAGE_KEY);
    if (!v) return null;
    const n = Number.parseInt(v, 10);
    return Number.isFinite(n) ? n : null;
  } catch {
    return null;
  }
}

export function TeamProvider({ children }: { children: ReactNode }) {
  const { user, isAdmin } = useAuth();
  const teams = useMemo(() => user?.teams ?? [], [user]);

  const [activeTeamId, setActiveTeamIdState] = useState<number | null>(() =>
    readStoredTeamId(),
  );

  // If the persisted team is no longer in the user's list, fall back to
  // the first team (teams are returned sorted alphabetically).
  useEffect(() => {
    if (teams.length === 0) {
      return;
    }
    const stillValid =
      activeTeamId != null && teams.some((t) => t.id === activeTeamId);
    if (!stillValid && teams[0]) {
      setActiveTeamIdState(teams[0].id);
    }
  }, [teams, activeTeamId]);

  useEffect(() => {
    try {
      if (activeTeamId == null) {
        window.localStorage.removeItem(STORAGE_KEY);
      } else {
        window.localStorage.setItem(STORAGE_KEY, String(activeTeamId));
      }
    } catch {
      // ignore storage errors (private mode, quota)
    }
  }, [activeTeamId]);

  const activeTeam =
    teams.find((t) => t.id === activeTeamId) ?? teams[0] ?? null;

  const isManager = (teamId?: number) => {
    if (isAdmin) return true;
    const target = teamId ?? activeTeam?.id;
    if (target == null) return false;
    return teams.some((t) => t.id === target && t.role === "manager");
  };

  return (
    <TeamContext.Provider
      value={{
        teams,
        activeTeam,
        activeTeamId: activeTeam?.id ?? null,
        setActiveTeamId: setActiveTeamIdState,
        isManager,
      }}
    >
      {children}
    </TeamContext.Provider>
  );
}

export function useTeam() {
  const ctx = useContext(TeamContext);
  if (!ctx) throw new Error("useTeam must be used within TeamProvider");
  return ctx;
}
