import { Outlet } from 'react-router-dom';
import Box from '@mui/material/Box';
import { useTheme } from '@mui/material/styles';
import { NavDrawerProvider, useNavDrawer, NAV_DRAWER_WIDTH } from '../../contexts/NavDrawerContext.tsx';
import { PageHeaderProvider } from '../../contexts/PageHeaderContext.tsx';
import { SharedHeader } from './SharedHeader.tsx';
import { NavigationDrawer } from './NavigationDrawer.tsx';

/**
 * App shell — OpenShift-style layout: one header spanning the full width
 * above everything, with a persistent left nav docked below it that pushes
 * the main content when open. Open/closed state is remembered in localStorage
 * via NavDrawerProvider.
 */
function AppLayoutShell() {
  const theme = useTheme();
  const { open } = useNavDrawer();

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', minHeight: '100vh' }}>
      <SharedHeader />
      <Box sx={{ display: 'flex', flexGrow: 1 }}>
        <NavigationDrawer />
        <Box
          component="main"
          sx={{
            flexGrow: 1,
            width: '100%',
            transition: theme.transitions.create('margin', {
              easing: open ? theme.transitions.easing.easeOut : theme.transitions.easing.sharp,
              duration: open
                ? theme.transitions.duration.enteringScreen
                : theme.transitions.duration.leavingScreen,
            }),
            marginLeft: open ? 0 : `-${NAV_DRAWER_WIDTH}px`,
          }}
        >
          <Outlet />
        </Box>
      </Box>
    </Box>
  );
}

export function AppLayout() {
  return (
    <NavDrawerProvider>
      <PageHeaderProvider>
        <AppLayoutShell />
      </PageHeaderProvider>
    </NavDrawerProvider>
  );
}
