import { Link, useLocation, useNavigate } from "react-router-dom";
import {
  Server,
  Plus,
  Settings,
  Users,
  LogOut,
  User,
  BarChart2,
  BookOpen,
  HardDrive,
  FolderOpen,
} from "lucide-react";
import { useHealth } from "@/hooks/useHealth";
import { useAuth } from "@/contexts/AuthContext";

export default function Sidebar() {
  const location = useLocation();
  const navigate = useNavigate();
  const { data: health } = useHealth();
  const { user, isAdmin, logout } = useAuth();

  const orchLabel =
    health?.orchestrator_backend === "kubernetes"
      ? "K8s"
      : health?.orchestrator_backend === "docker"
        ? "Docker"
        : null;
  const orchOk = health?.orchestrator === "connected";

  const handleLogout = async () => {
    await logout();
    navigate("/login");
  };

  const isActive = (path: string) => location.pathname === path;

  const navLinkClass = (path: string) =>
    `flex items-center gap-3 px-3 py-2.5 rounded-lg transition-colors ${
      isActive(path)
        ? "bg-blue-50 text-blue-700 font-medium"
        : "text-gray-600 hover:text-gray-900 hover:bg-gray-100"
    }`;

  return (
    <nav className="group fixed left-0 top-0 h-screen w-16 hover:w-56 transition-[width] duration-200 bg-white border-r border-gray-200 z-40 flex flex-col overflow-hidden">
      {/* Logo */}
      <div className="relative flex items-center gap-2 px-2 h-16 border-b border-gray-200 shrink-0">
        <img src="/favicon.svg" alt="Claworc" className="w-12 h-12 shrink-0" />
        <div className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden flex flex-col">
          <span className="text-sm font-semibold text-gray-900">Claworc</span>
          {orchLabel && (
            <span className="inline-flex items-center gap-1 text-xs font-medium text-gray-500">
              <span
                className={`inline-block w-1.5 h-1.5 rounded-full ${orchOk ? "bg-green-500" : "bg-red-500"}`}
              />
              {orchLabel}
            </span>
          )}
        </div>
        {/* Collapsed orchestrator dot — bottom-right of logo */}
        {orchLabel && (
          <span
            className={`absolute left-[50px] top-[50px] w-2.5 h-2.5 rounded-full border-2 border-white ${orchOk ? "bg-green-500" : "bg-red-500"} group-hover:opacity-0 transition-opacity duration-200`}
          />
        )}
      </div>

      {/* New Instance */}
      {isAdmin && (
        <div className="px-3 mt-3 shrink-0">
          <Link
            data-testid="new-instance-link"
            to="/instances/new"
            className="flex items-center gap-3 px-3 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 transition-colors"
          >
            <Plus size={18} className="shrink-0" />
            <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden">
              New Instance
            </span>
          </Link>
        </div>
      )}

      {/* Nav items */}
      <div className="flex flex-col gap-1 px-3 mt-4">
        <Link to="/" className={navLinkClass("/")}>
          <Server size={18} className="shrink-0" />
          <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
            Instances
          </span>
        </Link>
        {isAdmin && (
          <>
            <Link to="/usage" className={navLinkClass("/usage")}>
              <BarChart2 size={18} className="shrink-0" />
              <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
                Usage
              </span>
            </Link>
            <Link to="/skills" className={navLinkClass("/skills")}>
              <BookOpen size={18} className="shrink-0" />
              <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
                Skills
              </span>
            </Link>
          </>
        )}
        <Link to="/shared-folders" className={navLinkClass("/shared-folders")}>
          <FolderOpen size={18} className="shrink-0" />
          <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
            Shared Folders
          </span>
        </Link>
        {isAdmin && (
          <Link to="/backups" className={navLinkClass("/backups")}>
            <HardDrive size={18} className="shrink-0" />
            <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
              Backups
            </span>
          </Link>
        )}
      </div>

      {/* Spacer */}
      <div className="flex-grow" />

      {/* Build info */}
      {health && (
        <div className="px-4 pb-2 opacity-0 group-hover:opacity-100 transition-opacity duration-200">
          <span className="text-xs text-gray-400 whitespace-nowrap">
            {health.build_date
              ? `Built ${new Date(health.build_date).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric" })}`
              : "Build: DEV"}
          </span>
        </div>
      )}

      {/* Admin links below build info */}
      {isAdmin && (
        <div className="flex flex-col gap-1 px-3 pb-2">
          <Link to="/users" className={navLinkClass("/users")}>
            <Users size={18} className="shrink-0" />
            <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
              Users
            </span>
          </Link>
          <Link to="/settings" className={navLinkClass("/settings")}>
            <Settings size={18} className="shrink-0" />
            <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
              Settings
            </span>
          </Link>
        </div>
      )}

      {/* User section */}
      <div className="px-3 pb-4 border-t border-gray-200 pt-3 shrink-0">
        <Link
          to="/profile"
          className={`flex items-center gap-3 px-3 py-2.5 rounded-lg transition-colors ${
            isActive("/profile")
              ? "bg-blue-50 text-blue-700 font-medium"
              : "text-gray-600 hover:text-gray-900 hover:bg-gray-100"
          }`}
        >
          <User size={18} className="shrink-0" />
          <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
            {user?.username}
          </span>
        </Link>
        <button
          onClick={handleLogout}
          className="flex items-center gap-3 px-3 py-2.5 rounded-lg text-gray-600 hover:text-gray-900 hover:bg-gray-100 transition-colors w-full"
          title="Logout"
        >
          <LogOut size={18} className="shrink-0" />
          <span className="opacity-0 group-hover:opacity-100 transition-opacity duration-200 whitespace-nowrap overflow-hidden text-sm">
            Logout
          </span>
        </button>
      </div>
    </nav>
  );
}
