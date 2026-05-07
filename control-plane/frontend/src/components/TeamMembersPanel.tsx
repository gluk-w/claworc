import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2, Users as UsersIcon, Settings as SettingsIcon } from "lucide-react";
import {
  fetchTeamMembers,
  setTeamMember,
  removeTeamMember,
  fetchTeamProviderIDs,
  setTeamProviderIDs,
  type Team,
  type TeamMember,
} from "@/api/teams";
import { fetchUsers, type UserListItem } from "@/api/users";
import { useProviders } from "@/hooks/useProviders";
import { successToast, errorToast } from "@/utils/toast";

export default function TeamMembersPanel({ team }: { team: Team }) {
  const [tab, setTab] = useState<"members" | "providers">("members");
  return (
    <div>
      <div className="border-b border-gray-200 mb-4 flex gap-4">
        <TabButton active={tab === "members"} onClick={() => setTab("members")} icon={<UsersIcon size={14} />}>
          Members
        </TabButton>
        <TabButton active={tab === "providers"} onClick={() => setTab("providers")} icon={<SettingsIcon size={14} />}>
          Providers
        </TabButton>
      </div>
      {tab === "members" ? <MembersTab team={team} /> : <ProvidersTab team={team} />}
    </div>
  );
}

function TabButton({
  active,
  onClick,
  icon,
  children,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1.5 -mb-px py-2 text-sm border-b-2 ${
        active ? "border-blue-600 text-blue-700" : "border-transparent text-gray-500 hover:text-gray-700"
      }`}
    >
      {icon}
      {children}
    </button>
  );
}

function MembersTab({ team }: { team: Team }) {
  const qc = useQueryClient();
  const { data: members = [] } = useQuery({
    queryKey: ["teams", team.id, "members"],
    queryFn: () => fetchTeamMembers(team.id),
  });
  const { data: users = [] } = useQuery({ queryKey: ["users"], queryFn: fetchUsers });
  const [pickUser, setPickUser] = useState<number | "">("");
  const [pickRole, setPickRole] = useState<"user" | "manager">("user");

  const setMember = useMutation({
    mutationFn: ({ userId, role }: { userId: number; role: "user" | "manager" }) =>
      setTeamMember(team.id, { user_id: userId, role }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["teams", team.id, "members"] });
      qc.invalidateQueries({ queryKey: ["teams"] });
    },
    onError: (err) => errorToast("Failed", err),
  });
  const remove = useMutation({
    mutationFn: (userId: number) => removeTeamMember(team.id, userId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["teams", team.id, "members"] });
      qc.invalidateQueries({ queryKey: ["teams"] });
    },
    onError: (err) => errorToast("Failed to remove", err),
  });

  const memberIds = new Set(members.map((m) => m.user_id));
  const candidates = users.filter((u: UserListItem) => !memberIds.has(u.id) && u.role !== "admin");

  return (
    <div>
      <div className="flex gap-2 mb-3">
        <select
          value={pickUser}
          onChange={(e) => setPickUser(e.target.value ? Number(e.target.value) : "")}
          className="flex-1 border border-gray-300 rounded-md px-2 py-1 text-sm"
        >
          <option value="">Add member…</option>
          {candidates.map((u: UserListItem) => (
            <option key={u.id} value={u.id}>
              {u.username}
            </option>
          ))}
        </select>
        <select
          value={pickRole}
          onChange={(e) => setPickRole(e.target.value as "user" | "manager")}
          className="border border-gray-300 rounded-md px-2 py-1 text-sm"
        >
          <option value="user">User</option>
          <option value="manager">Manager</option>
        </select>
        <button
          type="button"
          disabled={!pickUser}
          onClick={() => {
            if (!pickUser) return;
            setMember.mutate({ userId: pickUser, role: pickRole });
            setPickUser("");
          }}
          className="px-3 py-1 text-sm text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
        >
          Add
        </button>
      </div>
      <table className="w-full text-sm">
        <thead className="text-xs text-gray-500 uppercase">
          <tr>
            <th className="text-left py-1.5">User</th>
            <th className="text-left py-1.5">Role</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {members.map((m: TeamMember) => (
            <tr key={m.user_id} className="border-t border-gray-100">
              <td className="py-1.5">{m.username}</td>
              <td className="py-1.5">
                <select
                  value={m.role}
                  onChange={(e) =>
                    setMember.mutate({ userId: m.user_id, role: e.target.value as "user" | "manager" })
                  }
                  className="border border-gray-300 rounded-md px-2 py-0.5 text-sm"
                >
                  <option value="user">User</option>
                  <option value="manager">Manager</option>
                </select>
              </td>
              <td className="py-1.5 text-right">
                <button
                  type="button"
                  onClick={() => {
                    if (window.confirm(`Remove ${m.username} from the team?`)) {
                      remove.mutate(m.user_id);
                    }
                  }}
                  className="text-red-600 hover:text-red-700"
                  title="Remove"
                >
                  <Trash2 size={14} />
                </button>
              </td>
            </tr>
          ))}
          {members.length === 0 && (
            <tr>
              <td colSpan={3} className="text-center text-gray-400 py-4">
                No members yet.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function ProvidersTab({ team }: { team: Team }) {
  const qc = useQueryClient();
  const { data: providers = [] } = useProviders();
  const { data: enabledIds = [] } = useQuery({
    queryKey: ["teams", team.id, "providers"],
    queryFn: () => fetchTeamProviderIDs(team.id),
  });

  const globalProviders = providers.filter((p) => !p.instance_id);
  const enabledSet = new Set(enabledIds);

  const save = useMutation({
    mutationFn: (ids: number[]) => setTeamProviderIDs(team.id, ids),
    onSuccess: () => {
      successToast("Providers updated");
      qc.invalidateQueries({ queryKey: ["teams", team.id, "providers"] });
    },
    onError: (err) => errorToast("Failed to update providers", err),
  });

  const toggle = (id: number) => {
    const next = new Set(enabledSet);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    save.mutate(Array.from(next));
  };

  return (
    <div>
      <p className="text-sm text-gray-500 mb-3">
        Choose which global LLM providers this team's instances may use. An
        empty list means no restriction (all global providers are available).
      </p>
      <ul className="divide-y divide-gray-100">
        {globalProviders.map((p) => (
          <li key={p.id} className="flex items-center justify-between py-2">
            <div>
              <div className="text-sm font-medium text-gray-800">{p.name}</div>
              <div className="text-xs text-gray-400">{p.key}</div>
            </div>
            <input
              type="checkbox"
              checked={enabledSet.has(p.id)}
              onChange={() => toggle(p.id)}
            />
          </li>
        ))}
        {globalProviders.length === 0 && (
          <li className="text-sm text-gray-400 py-4 text-center">
            No global providers configured yet.
          </li>
        )}
      </ul>
    </div>
  );
}
