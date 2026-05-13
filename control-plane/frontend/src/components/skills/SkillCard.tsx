import { Pencil, Trash2 } from "lucide-react";
import type { Skill, ClawhubResult } from "@/types/skills";

interface LibraryCardProps {
  skill: Skill;
  onDeploy: (slug: string, displayName: string) => void;
  onEdit?: (slug: string) => void;
  onDelete?: (slug: string) => void;
}

export function LibrarySkillCard({ skill, onDeploy, onEdit, onDelete }: LibraryCardProps) {
  return (
    <div
      className="bg-white border border-gray-200 rounded-xl p-4 flex flex-col gap-2 hover:shadow-sm hover:border-blue-300 hover:bg-blue-50 transition-all cursor-pointer"
      onClick={() => onDeploy(skill.slug, skill.name)}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-gray-900 truncate">{skill.name}</h3>
          <p className="text-xs text-gray-400 font-mono mt-0.5">{skill.slug}</p>
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <span className="text-xs text-gray-400 whitespace-nowrap">
            {new Date(skill.created_at).toLocaleDateString()}
          </span>
          {onEdit && (
            <button
              onClick={(e) => { e.stopPropagation(); onEdit(skill.slug); }}
              className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
              title="Edit"
            >
              <Pencil size={14} />
            </button>
          )}
          {onDelete && (
            <button
              onClick={(e) => { e.stopPropagation(); onDelete(skill.slug); }}
              className="p-1 text-gray-400 hover:text-red-600 transition-colors"
              title="Delete"
            >
              <Trash2 size={14} />
            </button>
          )}
        </div>
      </div>
      {skill.summary && (
        <p className="text-sm text-gray-600 line-clamp-2">{skill.summary}</p>
      )}
    </div>
  );
}

interface DiscoverCardProps {
  result: ClawhubResult;
  onDeploy: (slug: string, displayName: string, version: string) => void;
}

export function DiscoverSkillCard({ result, onDeploy }: DiscoverCardProps) {
  return (
    <div
      className="bg-white border border-gray-200 rounded-xl p-4 flex flex-col gap-2 hover:shadow-sm hover:border-blue-300 hover:bg-blue-50 transition-all cursor-pointer"
      onClick={() => onDeploy(result.slug, result.displayName, result.version)}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-gray-900 truncate">{result.displayName}</h3>
          <p className="text-xs text-gray-400 font-mono mt-0.5">{result.slug}</p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {result.version && (
            <span className="text-xs bg-gray-100 text-gray-500 px-2 py-0.5 rounded-full">
              v{result.version}
            </span>
          )}
          {result.updatedAt && (
            <span className="text-xs text-gray-400">
              {new Date(result.updatedAt).toLocaleDateString()}
            </span>
          )}
        </div>
      </div>
      {result.summary && (
        <p className="text-sm text-gray-600 line-clamp-2">{result.summary}</p>
      )}
    </div>
  );
}
