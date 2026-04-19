import { useState, useEffect, useRef } from "react";
import { Plus, Search, Loader2 } from "lucide-react";
import { useSkills, useDeleteSkill, useClawhubSearch } from "@/hooks/useSkills";
import { LibrarySkillCard, DiscoverSkillCard } from "@/components/skills/SkillCard";
import DeployModal from "@/components/skills/DeployModal";
import UploadSkillModal from "@/components/skills/UploadSkillModal";

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
  const [tab, setTab] = useState<Tab>("library");
  const [showUpload, setShowUpload] = useState(false);
  const [deployTarget, setDeployTarget] = useState<DeployTarget | null>(null);
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

  const handleDelete = (slug: string) => {
    if (!confirm(`Delete skill "${slug}"? This cannot be undone.`)) return;
    deleteSkill(slug);
  };

  const tabClass = (t: Tab) =>
    `px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
      tab === t
        ? "border-blue-600 text-blue-600"
        : "border-transparent text-gray-500 hover:text-gray-700"
    }`;

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Skills</h1>
        <button
          onClick={() => setShowUpload(true)}
          className={`flex items-center gap-2 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 ${tab !== "library" ? "invisible" : ""}`}
        >
          <Plus size={16} />
          Upload Skill
        </button>
      </div>

      {/* Tabs */}
      <div className="flex border-b border-gray-200 mb-6">
        <button className={tabClass("library")} onClick={() => setTab("library")}>
          Library
        </button>
        <button className={tabClass("discover")} onClick={() => setTab("discover")}>
          Discover
        </button>
      </div>

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
                  onDelete={handleDelete}
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
    </div>
  );
}
