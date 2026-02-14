import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { ThemeProvider } from '@mui/material/styles';
import CssBaseline from '@mui/material/CssBaseline';
import { theme } from './theme/index.ts';
import { AuthProvider } from './contexts/AuthContext.tsx';
import { SystemWarningBanner } from './components/layout/SystemWarningBanner.tsx';
import { VersionUpdateBanner } from './components/layout/VersionUpdateBanner.tsx';
import { DashboardPage } from './pages/DashboardPage.tsx';
import { SessionDetailPage } from './pages/SessionDetailPage.tsx';
import { TracePage } from './pages/TracePage.tsx';
import { SubmitAlertPage } from './pages/SubmitAlertPage.tsx';
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
    path: '/submit-alert',
    element: <SubmitAlertPage />,
  },
  {
    path: '*',
    element: <NotFoundPage />,
  },
]);

export function App() {
  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <AuthProvider>
        <SystemWarningBanner />
        <VersionUpdateBanner />
        <RouterProvider router={router} />
      </AuthProvider>
    </ThemeProvider>
  );
}
