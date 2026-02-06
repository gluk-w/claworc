import { Link, Outlet, useLocation } from "react-router-dom";
import { LayoutDashboard, Plus, Settings } from "lucide-react";

export default function Layout() {
  const location = useLocation();
  const isDashboard = location.pathname === "/";

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between h-14">
            <Link to="/" className="text-lg font-semibold text-gray-900">
              Openclaw Orchestrator
            </Link>
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
              <Link
                to="/settings"
                className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-700 border border-gray-300 rounded-md hover:bg-gray-50"
              >
                <Settings size={16} />
                Settings
              </Link>
              <Link
                to="/instances/new"
                className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
              >
                <Plus size={16} />
                New Instance
              </Link>
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
