import { Outlet } from "react-router-dom";
import Sidebar from "./Sidebar";
import TaskToasts from "./TaskToasts";
import AnalyticsConsentModal from "./AnalyticsConsentModal";
import { useOrchestratorWatcher } from "@/hooks/useOrchestratorWatcher";

export default function Layout() {
  useOrchestratorWatcher();
  return (
    <div className="min-h-screen bg-gray-50">
      <Sidebar />
      <main className="ml-16 min-h-screen px-4 sm:px-6 lg:px-8 pt-4 pb-4">
        <div className="max-w-7xl mx-auto">
          <Outlet />
        </div>
      </main>
      <TaskToasts />
      <AnalyticsConsentModal />
    </div>
  );
}
