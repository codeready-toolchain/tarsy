import { Outlet } from 'react-router-dom';
import Box from '@mui/material/Box';
import { NavDrawerProvider } from '../../contexts/NavDrawerContext.tsx';
import { PageHeaderProvider } from '../../contexts/PageHeaderContext.tsx';
import { SharedHeader } from './SharedHeader.tsx';
import { NavigationDrawer } from './NavigationDrawer.tsx';

/**
 * App shell — OpenShift-style layout: one header spanning the full width
 * above everything, with a left nav rail docked below it. The nav is always
 * visible; it's a flex sibling of the main content, so its own width
 * animation (icon-only rail <-> expanded with labels) naturally reflows the
 * main content without a separate margin hack. Open/collapsed state is
 * remembered in localStorage via NavDrawerProvider.
 */
function AppLayoutShell() {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', minHeight: '100vh' }}>
      <SharedHeader />
      <Box sx={{ display: 'flex', flexGrow: 1 }}>
        <NavigationDrawer />
        <Box component="main" sx={{ flexGrow: 1, width: '100%', minWidth: 0 }}>
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
