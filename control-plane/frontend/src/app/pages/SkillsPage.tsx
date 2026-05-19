import { useState, useEffect, useRef } from "react";
import { Search, Loader2 } from "lucide-react";
import { useSkills, useDeleteSkill, useClawhubSearch } from "@common/hooks/useSkills";
import { LibrarySkillCard, DiscoverSkillCard } from "@common/components/skills/SkillCard";
import DeployModal from "@common/components/skills/DeployModal";
import UploadSkillModal from "@common/components/skills/UploadSkillModal";
import SkillEditorModal from "@common/components/skills/SkillEditorModal";
import { useAuth } from "@common/contexts/AuthContext";
import Page from "@common/components/Page";

type Tab = "library" | "discover";

interface DeployTarget {
  slug: string;
  displayName: string;
  description?: string;
  source: "library" | "clawhub";
  version?: string;
  requiredEnvVars: string[];
}

export default function SkillsPage() {
  const { isAdmin } = useAuth();
  const [tab, setTab] = useState<Tab>("library");
  const [showUpload, setShowUpload] = useState(false);
  const [deployTarget, setDeployTarget] = useState<DeployTarget | null>(null);
  const [editSlug, setEditSlug] = useState<string | null>(null);
  const [searchInput, setSearchInput] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");

  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const { data: skills, isLoading: skillsLoading } = useSkills();
  const { mutate: deleteSkill } = useDeleteSkill();

  const {
    data: clawhubData,
    isLoading: clawhubLoading,
    isFetching: clawhubFetching,
  } = useClawhubSearch(debouncedSearch, tab === "discover");

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      setDebouncedSearch(searchInput);
    }, 300);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [searchInput]);

  const handleDeployLibrary = (slug: string, displayName: string) => {
    const skill = skills?.find((s) => s.slug === slug);
    setDeployTarget({
      slug,
      displayName,
      description: skill?.summary,
      source: "library",
      requiredEnvVars: skill?.required_env_vars ?? [],
    });
  };

  const handleDeployClawhub = (slug: string, displayName: string, version: string) => {
    const result = clawhubData?.results?.find((r) => r.slug === slug);
    // Clawhub search results don't expose required_env_vars; the backend will
    // still surface any missing names in the per-instance DeployResult.
    setDeployTarget({
      slug,
      displayName,
      description: result?.summary,
      source: "clawhub",
      version,
      requiredEnvVars: [],
    });
  };

  const handleEdit = (slug: string) => {
    setEditSlug(slug);
  };

  const handleDelete = (slug: string) => {
    if (!confirm(`Delete skill "${slug}"? This cannot be undone.`)) return;
    deleteSkill(slug);
  };


  return (
    <Page
      title="Skills"
      actions={
        isAdmin ? (
          <button
            onClick={() => setShowUpload(true)}
            className={`flex items-center gap-2 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 ${tab !== "library" ? "invisible" : ""}`}
          >
            Upload Skill
          </button>
        ) : undefined
      }
      tabs={
        isAdmin
          ? [
              { key: "library", label: "Library" },
              { key: "discover", label: "Discover" },
            ]
          : undefined
      }
      activeTab={tab}
      onTabChange={(k) => setTab(k as Tab)}
    >

      {/* Library Tab */}
      {tab === "library" && (
        <>
          {skillsLoading ? (
            <div className="flex items-center justify-center py-16">
              <Loader2 size={24} className="animate-spin text-gray-400" />
            </div>
          ) : !skills || skills.length === 0 ? (
            <div className="text-center py-16 text-gray-400">
              <p className="text-sm">No skills uploaded yet.</p>
              <p className="text-xs mt-1">Upload a .zip file containing a SKILL.md to get started.</p>
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
              {skills.map((skill) => (
                <LibrarySkillCard
                  key={skill.id}
                  skill={skill}
                  onDeploy={handleDeployLibrary}
                  onEdit={isAdmin ? handleEdit : undefined}
                  onDelete={isAdmin ? handleDelete : undefined}
                />
              ))}
            </div>
          )}
        </>
      )}

      {/* Discover Tab */}
      {tab === "discover" && (
        <>
          <div className="relative mb-6">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
            />
            <input
              type="text"
              placeholder="Search Clawhub skills…"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              className="w-full pl-9 pr-4 py-2.5 text-sm border border-gray-300 rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              autoFocus
            />
            {clawhubFetching && (
              <Loader2
                size={14}
                className="absolute right-3 top-1/2 -translate-y-1/2 animate-spin text-gray-400"
              />
            )}
          </div>

          {clawhubLoading && debouncedSearch ? (
            <div className="flex items-center justify-center py-16">
              <Loader2 size={24} className="animate-spin text-gray-400" />
            </div>
          ) : !debouncedSearch ? (
            <div className="text-center py-16 text-gray-400">
              <p className="text-sm">Search Clawhub for community skills to deploy.</p>
            </div>
          ) : !clawhubData?.results || clawhubData.results.length === 0 ? (
            <div className="text-center py-16 text-gray-400">
              <p className="text-sm">No results for "{debouncedSearch}".</p>
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
              {clawhubData.results.map((result) => (
                <DiscoverSkillCard
                  key={result.slug}
                  result={result}
                  onDeploy={handleDeployClawhub}
                />
              ))}
            </div>
          )}
        </>
      )}

      {/* Modals */}
      {showUpload && (
        <UploadSkillModal
          onClose={() => setShowUpload(false)}
          onUploaded={() => setShowUpload(false)}
        />
      )}
      {editSlug && (() => {
        const skill = skills?.find((s) => s.slug === editSlug);
        if (!skill) return null;
        return (
          <SkillEditorModal
            skill={skill}
            onClose={() => setEditSlug(null)}
          />
        );
      })()}
      {deployTarget && (
        <DeployModal
          slug={deployTarget.slug}
          displayName={deployTarget.displayName}
          description={deployTarget.description}
          source={deployTarget.source}
          version={deployTarget.version}
          requiredEnvVars={deployTarget.requiredEnvVars}
          onClose={() => setDeployTarget(null)}
        />
      )}
    </Page>
  );
}
