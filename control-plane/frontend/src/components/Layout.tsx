import { Link, Outlet, useLocation, useNavigate } from "react-router-dom";
import { LayoutDashboard, Plus, Settings, Users, LogOut } from "lucide-react";
import { useHealth } from "@/hooks/useHealth";
import { useAuth } from "@/contexts/AuthContext";

export default function Layout() {
  const location = useLocation();
  const navigate = useNavigate();
  const isDashboard = location.pathname === "/";
  const { data: health } = useHealth();
  const { user, isAdmin, logout } = useAuth();

  const orchLabel =
    health?.orchestrator_backend === "kubernetes"
      ? "Kubernetes"
      : health?.orchestrator_backend === "docker"
        ? "Docker"
        : null;
  const orchOk = health?.orchestrator === "connected";

  const handleLogout = async () => {
    await logout();
    navigate("/login");
  };

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between h-14">
            <div className="flex items-center gap-3">
              <Link to="/" className="text-lg font-semibold text-gray-900">
                OpenClaw Orchestrator
              </Link>
              {orchLabel && (
                <span className="inline-flex items-center gap-1.5 text-xs font-medium text-gray-600 bg-gray-100 px-2 py-0.5 rounded-full">
                  <span
                    className={`inline-block w-2 h-2 rounded-full ${orchOk ? "bg-green-500" : "bg-red-500"}`}
                  />
                  {orchLabel}
                </span>
              )}
            </div>
            <div className="flex items-center gap-3">
              <Link
                to="/"
                className={`inline-flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-md ${
                  isDashboard
                    ? "bg-gray-100 text-gray-900 font-medium"
                    : "text-gray-600 hover:text-gray-900 hover:bg-gray-50"
                }`}
              >
                <LayoutDashboard size={16} />
                Dashboard
              </Link>
              {isAdmin && (
                <>
                  <Link
                    data-testid="new-instance-link"
                    to="/instances/new"
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
                  >
                    <Plus size={16} />
                    New Instance
                  </Link>
                  <Link
                    to="/settings"
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-700 border border-gray-300 rounded-md hover:bg-gray-50"
                  >
                    <Settings size={16} />
                    Settings
                  </Link>
                  <Link
                    to="/users"
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-700 border border-gray-300 rounded-md hover:bg-gray-50"
                  >
                    <Users size={16} />
                    Users
                  </Link>
                </>
              )}
              <div className="flex items-center gap-2 ml-2 pl-3 border-l border-gray-200">
                <Link
                  to="/account"
                  className="text-sm text-gray-600 hover:text-gray-900"
                >
                  {user?.username}
                </Link>
                <button
                  onClick={handleLogout}
                  className="p-1.5 text-gray-400 hover:text-gray-600 rounded"
                  title="Logout"
                >
                  <LogOut size={16} />
                </button>
              </div>
            </div>
          </div>
        </div>
      </header>
      <main className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6">
        <Outlet />
      </main>
    </div>
  );
}
