import { Routes, Route, Navigate } from "react-router-dom";
import Layout from "./components/Layout";
import DashboardPage from "./pages/DashboardPage";
import CreateInstancePage from "./pages/CreateInstancePage";
import InstanceDetailPage from "./pages/InstanceDetailPage";
import SettingsPage from "./pages/SettingsPage";
import LoginPage from "./pages/LoginPage";
import UsersPage from "./pages/UsersPage";
import UsagePage from "./pages/UsagePage";
import AccountPage from "./pages/AccountPage";
import VncPopupPage from "./pages/VncPopupPage";
import ChatPopupPage from "./pages/ChatPopupPage";
import SkillsPage from "./pages/SkillsPage";
import BackupsPage from "./pages/BackupsPage";
import SharedFoldersPage from "./pages/SharedFoldersPage";
import { useAuth } from "./contexts/AuthContext";

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { user, isLoading } = useAuth();
  if (isLoading) return null;
  if (!user) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

function AdminRoute({ children }: { children: React.ReactNode }) {
  const { isAdmin, isLoading } = useAuth();
  if (isLoading) return null;
  if (!isAdmin) return <Navigate to="/" replace />;
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
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
            <AdminRoute>
              <CreateInstancePage />
            </AdminRoute>
          }
        />
        <Route path="/instances/:id" element={<InstanceDetailPage />} />
        <Route path="/shared-folders" element={<SharedFoldersPage />} />
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
            <AdminRoute>
              <SkillsPage />
            </AdminRoute>
          }
        />
        <Route
          path="/backups"
          element={
            <AdminRoute>
              <BackupsPage />
            </AdminRoute>
          }
        />
      </Route>
    </Routes>
  );
}
