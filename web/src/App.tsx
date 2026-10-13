import { QueryClientProvider, useQuery, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Route, Routes } from "react-router";
import { AppShell } from "./components/layout/AppShell";
import { ToastProvider } from "./components/toast";
import { ErrorState, Spinner } from "./components/ui/feedback";
import { LiveProvider } from "./hooks/live";
import { api } from "./lib/api";
import { ComparePage } from "./pages/Compare";
import { DocsPage } from "./pages/Docs";
import { EndpointDetailPage } from "./pages/EndpointDetail";
import { EndpointsPage } from "./pages/Endpoints";
import { LoginPage } from "./pages/Login";
import { NotFoundPage } from "./pages/NotFound";
import { OverviewPage } from "./pages/Overview";
import { ReplayPage } from "./pages/Replay";
import { RequestDetailPage } from "./pages/RequestDetail";
import { RequestsPage } from "./pages/Requests";
import { SettingsPage } from "./pages/Settings";

function AuthGate({ children }: { children: ReactNode }) {
  const session = useQuery({ queryKey: ["session"], queryFn: api.session, staleTime: Infinity, retry: 1 });
  if (session.isPending) {
    return (
      <div className="flex min-h-dvh items-center justify-center">
        <Spinner label="Loading Relaybox" />
      </div>
    );
  }
  if (session.isError) {
    return (
      <div className="flex min-h-dvh items-center justify-center">
        <ErrorState
          error={session.error}
          title="Relaybox is not reachable"
          onRetry={() => session.refetch()}
        />
      </div>
    );
  }
  if (session.data.auth_required && !session.data.authenticated) return <LoginPage />;
  return <LiveProvider>{children}</LiveProvider>;
}

export function App({ queryClient }: { queryClient: QueryClient }) {
  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <AuthGate>
          <Routes>
            <Route element={<AppShell />}>
              <Route index element={<OverviewPage />} />
              <Route path="endpoints" element={<EndpointsPage />} />
              <Route path="endpoints/:id" element={<EndpointDetailPage />} />
              <Route path="requests" element={<RequestsPage />} />
              <Route path="requests/:id" element={<RequestDetailPage />} />
              <Route path="requests/:id/replay" element={<ReplayPage />} />
              <Route path="compare" element={<ComparePage />} />
              <Route path="settings" element={<SettingsPage />} />
              <Route path="docs" element={<DocsPage />} />
              <Route path="*" element={<NotFoundPage />} />
            </Route>
          </Routes>
        </AuthGate>
      </ToastProvider>
    </QueryClientProvider>
  );
}
