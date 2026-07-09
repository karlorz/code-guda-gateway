import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { SessionProvider, useSession } from './api/session';
import { AdminLayout } from './routes/AdminLayout';
import { AuditPage } from './features/audit/AuditPage';
import { DebugAttemptsPage } from './features/debug/DebugAttemptsPage';
import { GatewayKeysPage } from './features/gateway-keys/GatewayKeysPage';
import { LoginPage } from './features/login/LoginPage';
import { OverviewPage } from './features/overview/OverviewPage';
import { ProviderKeysPage } from './features/provider-keys/ProviderKeysPage';
import { ProvidersPage } from './features/providers/ProvidersPage';
import { SettingsPage } from './features/settings/SettingsPage';
import { UsagePage } from './features/usage/UsagePage';

const queryClient = new QueryClient();

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <SessionProvider>
        <BrowserRouter basename="/admin">
          <AppRoutes />
        </BrowserRouter>
      </SessionProvider>
    </QueryClientProvider>
  );
}

function AppRoutes() {
  const { authenticated, loading } = useSession();
  if (loading) {
    return <div className="grid min-h-screen place-items-center bg-zinc-50 text-sm text-zinc-500">Loading</div>;
  }
  if (!authenticated) {
    return <LoginPage />;
  }
  return (
    <Routes>
      <Route element={<AdminLayout />} path="/">
        <Route element={<OverviewPage />} index />
        <Route element={<GatewayKeysPage />} path="gateway-keys" />
        <Route element={<ProviderKeysPage />} path="provider-keys" />
        <Route element={<ProvidersPage />} path="providers" />
        <Route element={<UsagePage />} path="usage" />
        <Route element={<AuditPage />} path="audit" />
        <Route element={<SettingsPage />} path="settings" />
        <Route element={<DebugAttemptsPage />} path="debug/attempts" />
        <Route element={<Navigate replace to="/" />} path="*" />
      </Route>
    </Routes>
  );
}
