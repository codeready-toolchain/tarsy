import { useMemo } from 'react';
import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { ThemeProvider } from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import { buildTheme } from './theme/index.ts';
import { ColorModeProvider, useColorMode } from './contexts/ColorModeContext.tsx';
import { AuthProvider } from './contexts/AuthContext.tsx';
import { VersionProvider } from './contexts/VersionContext.tsx';
import { SystemWarningBanner } from './components/layout/SystemWarningBanner.tsx';
import { VersionUpdateBanner } from './components/layout/VersionUpdateBanner.tsx';
import { DashboardPage } from './pages/DashboardPage.tsx';
import { SessionDetailPage } from './pages/SessionDetailPage.tsx';
import { TracePage } from './pages/TracePage.tsx';
import { SubmitAlertPage } from './pages/SubmitAlertPage.tsx';
import { SystemStatusPage } from './pages/SystemStatusPage.tsx';
import { ScoringPage } from './pages/ScoringPage.tsx';
import { NotFoundPage } from './pages/NotFoundPage.tsx';

const router = createBrowserRouter([
  {
    path: '/',
    element: <DashboardPage />,
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
    path: '*',
    element: <NotFoundPage />,
  },
]);

function ThemedApp() {
  const { mode } = useColorMode();
  const theme = useMemo(() => buildTheme(mode), [mode]);

  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
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

export function App() {
  return (
    <ColorModeProvider>
      <ThemedApp />
    </ColorModeProvider>
  );
}
