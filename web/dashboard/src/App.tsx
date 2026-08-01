import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { ThemeProvider } from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import { theme } from './theme/index.ts';
import { AuthProvider } from './contexts/AuthContext.tsx';
import { VersionProvider } from './contexts/VersionContext.tsx';
import { SystemWarningBanner } from './components/layout/SystemWarningBanner.tsx';
import { VersionUpdateBanner } from './components/layout/VersionUpdateBanner.tsx';
import { AppLayout } from './components/layout/AppLayout.tsx';
import { DashboardView } from './components/dashboard/DashboardView.tsx';
import { SessionDetailPage } from './pages/SessionDetailPage.tsx';
import { TracePage } from './pages/TracePage.tsx';
import { SubmitAlertPage } from './pages/SubmitAlertPage.tsx';
import { SystemStatusPage } from './pages/SystemStatusPage.tsx';
import { ScoringPage } from './pages/ScoringPage.tsx';
import { UsagePage } from './pages/UsagePage.tsx';
import { NotFoundPage } from './pages/NotFoundPage.tsx';

const router = createBrowserRouter([
  {
    element: <AppLayout />,
    children: [
      {
        // DashboardView is used directly (not via a wrapper page) so that
        // React Router reuses the same component instance when navigating
        // between "/" and "/triage" instead of remounting it — that keeps
        // loaded sessions/filters and the WebSocket subscription intact
        // across tab switches. See DashboardView's triageFetchedRef effect
        // for how it still loads triage data when switching into that tab.
        path: '/',
        element: <DashboardView tab="sessions" />,
      },
      {
        path: '/triage',
        element: <DashboardView tab="triage" />,
      },
      {
        path: '/sessions/:id',
        element: <SessionDetailPage />,
      },
      {
        path: '/sessions/:id/trace',
        element: <TracePage />,
      },
      {
        path: '/sessions/:id/scoring',
        element: <ScoringPage />,
      },
      {
        path: '/submit-alert',
        element: <SubmitAlertPage />,
      },
      {
        path: '/system',
        element: <SystemStatusPage />,
      },
      {
        path: '/usage',
        element: <UsagePage />,
      },
      {
        path: '*',
        element: <NotFoundPage />,
      },
    ],
  },
]);

export function App() {
  return (
    <ThemeProvider theme={theme} defaultMode="light">
      <CssBaseline enableColorScheme />
      <VersionProvider>
        <AuthProvider>
          <VersionUpdateBanner />
          <SystemWarningBanner />
          <RouterProvider router={router} />
        </AuthProvider>
      </VersionProvider>
    </ThemeProvider>
  );
}
