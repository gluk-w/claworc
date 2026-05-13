import { Routes, Route, Navigate } from "react-router-dom";
import Layout from "./components/Layout";
import DashboardPage from "./pages/DashboardPage";
import CreateInstancePage from "./pages/CreateInstancePage";
import InstanceDetailPage from "./pages/InstanceDetailPage";
import SettingsPage from "./pages/SettingsPage";
import LoginPage from "./pages/LoginPage";
import BackendUnavailablePage from "./pages/BackendUnavailablePage";
import UsersPage from "./pages/UsersPage";
import TeamsPage from "./pages/TeamsPage";
import UsagePage from "./pages/UsagePage";
import AccountPage from "./pages/AccountPage";
import VncPopupPage from "./pages/VncPopupPage";
import ChatPopupPage from "./pages/ChatPopupPage";
import SkillsPage from "./pages/SkillsPage";
import BackupsPage from "./pages/BackupsPage";
import SharedFoldersPage from "./pages/SharedFoldersPage";
import KanbanPage from "./pages/KanbanPage";
import { useAuth } from "./contexts/AuthContext";

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { user, isLoading, isBackendUnavailable } = useAuth();
  if (isLoading) return null;
  if (isBackendUnavailable) return <BackendUnavailablePage />;
  if (!user) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

function AdminRoute({ children }: { children: React.ReactNode }) {
  const { isAdmin, isLoading, isBackendUnavailable } = useAuth();
  if (isLoading) return null;
  if (isBackendUnavailable) return <BackendUnavailablePage />;
  if (!isAdmin) return <Navigate to="/" replace />;
  return <>{children}</>;
}

function LoginRoute() {
  const { isBackendUnavailable, isLoading } = useAuth();
  if (isLoading) return null;
  if (isBackendUnavailable) return <BackendUnavailablePage />;
  return <LoginPage />;
}

function InstanceCreatorRoute({ children }: { children: React.ReactNode }) {
  const { canCreateInstances, isLoading, isBackendUnavailable } = useAuth();
  if (isLoading) return null;
  if (isBackendUnavailable) return <BackendUnavailablePage />;
  if (!canCreateInstances) return <Navigate to="/" replace />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginRoute />} />
      <Route
        path="/instances/:id/vnc"
        element={
          <ProtectedRoute>
            <VncPopupPage />
          </ProtectedRoute>
        }
      />
      <Route
        path="/instances/:id/chat"
        element={
          <ProtectedRoute>
            <ChatPopupPage />
          </ProtectedRoute>
        }
      />
      <Route
        element={
          <ProtectedRoute>
            <Layout />
          </ProtectedRoute>
        }
      >
        <Route path="/" element={<DashboardPage />} />
        <Route
          path="/instances/new"
          element={
            <InstanceCreatorRoute>
              <CreateInstancePage />
            </InstanceCreatorRoute>
          }
        />
        <Route path="/instances/:id" element={<InstanceDetailPage />} />
        <Route path="/shared-folders" element={<SharedFoldersPage />} />
        <Route path="/kanban" element={<KanbanPage />} />
        <Route path="/profile" element={<AccountPage />} />
        <Route
          path="/settings"
          element={
            <AdminRoute>
              <SettingsPage />
            </AdminRoute>
          }
        />
        <Route
          path="/users"
          element={
            <AdminRoute>
              <UsersPage />
            </AdminRoute>
          }
        />
        <Route
          path="/teams"
          element={
            <AdminRoute>
              <TeamsPage />
            </AdminRoute>
          }
        />
        <Route
          path="/usage"
          element={
            <AdminRoute>
              <UsagePage />
            </AdminRoute>
          }
        />
        <Route
          path="/skills"
          element={
            <ProtectedRoute>
              <SkillsPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/backups"
          element={
            <ProtectedRoute>
              <BackupsPage />
            </ProtectedRoute>
          }
        />
      </Route>
    </Routes>
  );
}
