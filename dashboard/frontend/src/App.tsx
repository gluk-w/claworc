import { Routes, Route } from "react-router-dom";
import Layout from "./components/Layout";
import DashboardPage from "./pages/DashboardPage";
import CreateInstancePage from "./pages/CreateInstancePage";
import InstanceDetailPage from "./pages/InstanceDetailPage";
import SettingsPage from "./pages/SettingsPage";

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/instances/new" element={<CreateInstancePage />} />
        <Route path="/instances/:id" element={<InstanceDetailPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Route>
    </Routes>
  );
}
