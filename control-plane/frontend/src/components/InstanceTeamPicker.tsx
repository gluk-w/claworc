import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown } from "lucide-react";

export interface PickerInstance {
  id: number;
  name: string;
  display_name?: string;
  team_id: number;
}

export interface PickerTeam {
  id: number;
  name: string;
}

export type SingleSelection =
  | { kind: "all" }
  | { kind: "team"; teamId: number }
  | { kind: "instance"; instanceId: number };

type SingleProps = {
  mode: "single";
  instances: PickerInstance[];
  teams: PickerTeam[];
  selected: SingleSelection;
  onChange: (sel: SingleSelection) => void;
  allowAll?: boolean;
  allowTeamSelect?: boolean;
  placeholder?: string;
  className?: string;
};

type MultiProps = {
  mode: "multi";
  instances: PickerInstance[];
  teams: PickerTeam[];
  selectedInstanceIds: number[];
  onChange: (ids: number[]) => void;
  /**
   * When provided (together with `onTeamsChange`), the picker enters
   * team-or-instance mode: team-header checkboxes toggle persistent team
   * selection, and instances under a selected team render as auto-covered
   * (checked + disabled). When omitted, the team-header checkbox is a plain
   * tri-state bulk-toggle of the underlying instance checkboxes.
   */
  selectedTeamIds?: number[];
  onTeamsChange?: (ids: number[]) => void;
  placeholder?: string;
  className?: string;
};

type Props = SingleProps | MultiProps;

interface TeamGroup {
  teamId: number | null;
  teamName: string;
  instances: PickerInstance[];
}

function instanceLabel(inst: PickerInstance): string {
  return inst.display_name || inst.name;
}

function groupInstancesByTeam(
  instances: PickerInstance[],
  teams: PickerTeam[],
): TeamGroup[] {
  const teamName = new Map<number, string>(teams.map((t) => [t.id, t.name]));
  const byTeam = new Map<number | null, PickerInstance[]>();
  for (const inst of instances) {
    const key = teamName.has(inst.team_id) ? inst.team_id : null;
    const list = byTeam.get(key) ?? [];
    list.push(inst);
    byTeam.set(key, list);
  }
  // Preserve incoming team order; trailing "Other" bucket for unknown team_ids.
  const groups: TeamGroup[] = [];
  for (const t of teams) {
    const list = byTeam.get(t.id);
    if (list && list.length > 0) {
      list.sort((a, b) => instanceLabel(a).localeCompare(instanceLabel(b)));
      groups.push({ teamId: t.id, teamName: t.name, instances: list });
    }
  }
  const orphan = byTeam.get(null);
  if (orphan && orphan.length > 0) {
    orphan.sort((a, b) => instanceLabel(a).localeCompare(instanceLabel(b)));
    groups.push({ teamId: null, teamName: "Other", instances: orphan });
  }
  return groups;
}

export default function InstanceTeamPicker(props: Props) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);

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

  const groups = useMemo(
    () => groupInstancesByTeam(props.instances, props.teams),
    [props.instances, props.teams],
  );

  const triggerLabel = useMemo(() => {
    if (props.mode === "single") {
      const sel = props.selected;
      if (sel.kind === "instance") {
        const inst = props.instances.find((i) => i.id === sel.instanceId);
        return inst ? instanceLabel(inst) : props.placeholder ?? "Instance";
      }
      if (sel.kind === "team") {
        const team = props.teams.find((t) => t.id === sel.teamId);
        return team ? team.name : props.placeholder ?? "Team";
      }
      return props.placeholder ?? "All instances";
    }
    // multi
    const instanceIds = props.selectedInstanceIds;
    const teamIds = props.selectedTeamIds ?? [];
    if (teamIds.length === 0 && instanceIds.length === 0) {
      return props.placeholder ?? "Select instances...";
    }
    const teamNames = teamIds
      .map((id) => props.teams.find((t) => t.id === id)?.name)
      .filter((n): n is string => Boolean(n));
    if (teamIds.length > 0 && instanceIds.length === 0) {
      return teamNames.join(", ");
    }
    if (teamIds.length === 0) {
      if (instanceIds.length === 1) {
        const inst = props.instances.find((i) => i.id === instanceIds[0]);
        return inst ? instanceLabel(inst) : "1 selected";
      }
      return `${instanceIds.length} selected`;
    }
    const noun = instanceIds.length === 1 ? "instance" : "instances";
    return `${teamNames.join(", ")} + ${instanceIds.length} ${noun}`;
  }, [props]);

  return (
    <div ref={wrapRef} className={`relative ${props.className ?? ""}`}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        title={triggerLabel}
        className="inline-flex w-full items-center justify-between gap-2 min-w-[14rem] border border-gray-300 rounded-md px-3 py-1.5 text-sm text-gray-700 bg-white focus:outline-none focus:ring-2 focus:ring-blue-500"
      >
        <span className="truncate">{triggerLabel}</span>
        <ChevronDown size={14} className="text-gray-400 shrink-0" />
      </button>
      {open && (
        <div className="absolute left-0 mt-1 w-72 max-h-80 overflow-auto bg-white border border-gray-200 rounded-md shadow-lg z-20 py-1">
          {props.mode === "single" ? (
            <SingleBody
              {...props}
              groups={groups}
              onCommit={() => setOpen(false)}
            />
          ) : (
            <MultiBody {...props} groups={groups} />
          )}
        </div>
      )}
    </div>
  );
}

function SingleBody({
  groups,
  selected,
  onChange,
  onCommit,
  allowAll = true,
  allowTeamSelect = true,
}: SingleProps & { groups: TeamGroup[]; onCommit: () => void }) {
  const allActive = selected.kind === "all";
  return (
    <>
      {allowAll && (
        <button
          type="button"
          onClick={() => {
            onChange({ kind: "all" });
            onCommit();
          }}
          className={`w-full text-left px-3 py-1.5 text-sm hover:bg-gray-50 ${
            allActive ? "bg-gray-100" : ""
          }`}
        >
          All instances
        </button>
      )}
      {groups.map((group) => {
        const teamActive =
          allowTeamSelect &&
          selected.kind === "team" &&
          group.teamId != null &&
          selected.teamId === group.teamId;
        const headerClass = `w-full text-left px-3 py-1.5 text-sm font-bold ${
          teamActive ? "bg-gray-100" : ""
        } ${allowTeamSelect ? "hover:bg-gray-50" : ""}`;
        return (
          <div key={group.teamId ?? "other"}>
            {allowTeamSelect && group.teamId != null ? (
              <button
                type="button"
                onClick={() => {
                  onChange({ kind: "team", teamId: group.teamId! });
                  onCommit();
                }}
                className={headerClass}
              >
                {group.teamName}
              </button>
            ) : (
              <div className={headerClass}>{group.teamName}</div>
            )}
            {group.instances.map((inst) => {
              const active =
                selected.kind === "instance" && selected.instanceId === inst.id;
              return (
                <button
                  key={inst.id}
                  type="button"
                  onClick={() => {
                    onChange({ kind: "instance", instanceId: inst.id });
                    onCommit();
                  }}
                  className={`w-full text-left pl-7 pr-3 py-1.5 text-sm hover:bg-gray-50 ${
                    active ? "bg-gray-100" : ""
                  }`}
                >
                  {instanceLabel(inst)}
                </button>
              );
            })}
          </div>
        );
      })}
    </>
  );
}

function MultiBody({
  groups,
  selectedInstanceIds,
  onChange,
  selectedTeamIds,
  onTeamsChange,
}: MultiProps & { groups: TeamGroup[] }) {
  const selectedSet = useMemo(
    () => new Set(selectedInstanceIds),
    [selectedInstanceIds],
  );
  const teamSet = useMemo(
    () => new Set(selectedTeamIds ?? []),
    [selectedTeamIds],
  );
  const showHeadings = groups.length > 1;
  // When the caller wires up team-level selection, team headers persist that
  // selection (vs. the legacy mode where the header checkbox is a bulk-toggle
  // of the underlying instance checkboxes).
  const teamSelectionEnabled = Boolean(onTeamsChange);

  const toggleInstance = (id: number) => {
    const next = new Set(selectedSet);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    onChange(Array.from(next));
  };

  const setGroupChecked = (group: TeamGroup, checked: boolean) => {
    if (teamSelectionEnabled && group.teamId != null && onTeamsChange) {
      const next = new Set(teamSet);
      if (checked) next.add(group.teamId);
      else next.delete(group.teamId);
      onTeamsChange(Array.from(next));
      // Also drop any individual instance IDs from this team now that it's
      // covered as a unit; clean state avoids stale entries when persisting.
      if (checked) {
        const drop = new Set(group.instances.map((i) => i.id));
        const filtered = Array.from(selectedSet).filter((id) => !drop.has(id));
        if (filtered.length !== selectedSet.size) onChange(filtered);
      }
      return;
    }
    // Legacy bulk-toggle of individual instance checkboxes.
    const next = new Set(selectedSet);
    for (const inst of group.instances) {
      if (checked) next.add(inst.id);
      else next.delete(inst.id);
    }
    onChange(Array.from(next));
  };

  return (
    <>
      {groups.map((group) => {
        const groupIds = group.instances.map((i) => i.id);
        const teamPicked =
          teamSelectionEnabled &&
          group.teamId != null &&
          teamSet.has(group.teamId);
        const selectedCount = teamPicked
          ? groupIds.length
          : groupIds.filter((id) => selectedSet.has(id)).length;
        const allSelected =
          groupIds.length > 0 && selectedCount === groupIds.length;
        const indeterminate = selectedCount > 0 && !allSelected;
        return (
          <div key={group.teamId ?? "other"}>
            {showHeadings && (
              <label
                className="flex items-center gap-2 px-3 py-1.5 text-sm font-bold cursor-pointer hover:bg-gray-50"
                onClick={(e) => e.stopPropagation()}
              >
                <input
                  type="checkbox"
                  checked={allSelected}
                  disabled={teamSelectionEnabled && group.teamId == null}
                  ref={(el) => {
                    if (el) el.indeterminate = indeterminate;
                  }}
                  onChange={(e) => setGroupChecked(group, e.target.checked)}
                  className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                />
                <span className="truncate">{group.teamName}</span>
              </label>
            )}
            {group.instances.map((inst) => {
              const coveredByTeam = teamPicked;
              const active = coveredByTeam || selectedSet.has(inst.id);
              const rowTitle = coveredByTeam
                ? `Covered by team "${group.teamName}"`
                : undefined;
              return (
                <label
                  key={inst.id}
                  title={rowTitle}
                  className={`flex items-center gap-2 ${
                    showHeadings ? "pl-7 pr-3" : "px-3"
                  } py-1.5 text-sm hover:bg-gray-50 ${
                    coveredByTeam
                      ? "cursor-not-allowed text-gray-500"
                      : "cursor-pointer"
                  } ${active ? "bg-gray-50" : ""}`}
                >
                  <input
                    type="checkbox"
                    checked={active}
                    disabled={coveredByTeam}
                    onChange={() => toggleInstance(inst.id)}
                    className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  <span className="truncate">{instanceLabel(inst)}</span>
                </label>
              );
            })}
          </div>
        );
      })}
    </>
  );
}
