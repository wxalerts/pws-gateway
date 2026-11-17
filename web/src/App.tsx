import { Navigate, Route, Routes } from 'react-router-dom';
import { RequireAuth } from './auth/AuthContext';
import { Layout } from './components/Layout';
import { AccountPage } from './pages/Account';
import { AboutPage } from './pages/About';
import { DashboardPage } from './pages/Dashboard';
import { HomeAssistantPage } from './pages/HomeAssistant';
import { LoginPage } from './pages/Login';
import { LogsPage } from './pages/Logs';
import { NetworkPage } from './pages/Network';
import { RadiosPage } from './pages/Radios';
import { RelayPage } from './pages/Relay';
import { SetupPage } from './pages/Setup';
import { StationDetailPage } from './pages/StationDetail';
import { StationsPage } from './pages/Stations';
import { UpdatesPage } from './pages/Updates';
import { UplinkPage } from './pages/Uplink';

export function App() {
  return (
    <Routes>
      <Route path="/setup" element={<SetupPage />} />
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<DashboardPage />} />
        <Route path="stations" element={<StationsPage />} />
        <Route path="stations/:key" element={<StationDetailPage />} />
        <Route path="radios" element={<RadiosPage />} />
        <Route path="relay" element={<RelayPage />} />
        <Route path="account" element={<AccountPage />} />
        <Route path="uplink" element={<UplinkPage />} />
        <Route path="updates" element={<UpdatesPage />} />
        <Route path="network" element={<NetworkPage />} />
        <Route path="home-assistant" element={<HomeAssistantPage />} />
        <Route path="logs" element={<LogsPage />} />
        <Route path="about" element={<AboutPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
