import type { ReactNode } from 'react';
import { Link as RouterLink, useLocation } from 'react-router-dom';
import Drawer from '@mui/material/Drawer';
import List from '@mui/material/List';
import ListItem from '@mui/material/ListItem';
import ListItemButton from '@mui/material/ListItemButton';
import ListItemIcon from '@mui/material/ListItemIcon';
import ListItemText from '@mui/material/ListItemText';
import Tooltip from '@mui/material/Tooltip';
import { useTheme } from '@mui/material/styles';
import useMediaQuery from '@mui/material/useMediaQuery';
import HomeIcon from '@mui/icons-material/Home';
import FactCheckIcon from '@mui/icons-material/FactCheck';
import SendIcon from '@mui/icons-material/Send';
import BarChartIcon from '@mui/icons-material/BarChart';
import DnsIcon from '@mui/icons-material/Dns';
import { ROUTES } from '../../constants/routes.ts';
import { useNavDrawer, NAV_DRAWER_WIDTH, NAV_DRAWER_WIDTH_COLLAPSED } from '../../contexts/NavDrawerContext.tsx';

const NAV_ITEMS = [
  {
    label: 'Alert Investigations',
    to: ROUTES.SESSIONS,
    icon: <HomeIcon fontSize="small" />,
    match: (pathname: string) => pathname === ROUTES.SESSIONS,
  },
  {
    label: 'Triage',
    to: ROUTES.TRIAGE,
    icon: <FactCheckIcon fontSize="small" />,
    match: (pathname: string) => pathname === ROUTES.TRIAGE,
  },
  {
    label: 'Submit Alert',
    to: ROUTES.SUBMIT_ALERT,
    icon: <SendIcon fontSize="small" />,
    match: (pathname: string) => pathname.startsWith(ROUTES.SUBMIT_ALERT),
  },
  {
    label: 'Usage',
    to: ROUTES.USAGE,
    icon: <BarChartIcon fontSize="small" />,
    match: (pathname: string) => pathname.startsWith(ROUTES.USAGE),
  },
  {
    label: 'System Status',
    to: ROUTES.SYSTEM_STATUS,
    icon: <DnsIcon fontSize="small" />,
    match: (pathname: string) => pathname.startsWith(ROUTES.SYSTEM_STATUS),
  },
] as const;

/** Shared nav item list — rendered either as the desktop mini-variant rail or the mobile overlay's full-labeled list. */
function NavList({
  expanded,
  onItemClick,
}: {
  expanded: boolean;
  onItemClick?: () => void;
}): ReactNode {
  const { pathname } = useLocation();
  const theme = useTheme();

  return (
    <List sx={{ py: 1 }}>
      {NAV_ITEMS.map((item) => {
        const selected = item.match(pathname);
        const button = (
          <ListItemButton
            component={RouterLink}
            to={item.to}
            selected={selected}
            onClick={onItemClick}
            sx={{
              minHeight: 44,
              justifyContent: expanded ? 'flex-start' : 'center',
              px: 2.5,
            }}
          >
            <ListItemIcon sx={{ minWidth: 0, mr: expanded ? 2 : 0, justifyContent: 'center' }}>
              {item.icon}
            </ListItemIcon>
            <ListItemText
              primary={item.label}
              sx={{
                opacity: expanded ? 1 : 0,
                transition: theme.transitions.create('opacity', {
                  duration: theme.transitions.duration.shortest,
                }),
              }}
            />
          </ListItemButton>
        );

        return (
          <ListItem key={item.to} disablePadding sx={{ display: 'block' }}>
            {expanded ? button : (
              <Tooltip title={item.label} placement="right">
                {button}
              </Tooltip>
            )}
          </ListItem>
        );
      })}
    </List>
  );
}

/**
 * Left navigation. On desktop it's a permanent mini-variant rail docked below
 * the header — always visible, collapsed to icons by default and expanding in
 * place to show labels too (open/collapsed state owned by NavDrawerContext,
 * remembered across routes). On mobile a permanent rail would permanently
 * steal ~64px from an already-narrow viewport, so it instead becomes a
 * temporary overlay: hidden by default, opened by the same hamburger button,
 * shown full-width-labeled over the content, and dismissed on backdrop click
 * or after picking a destination.
 */
export function NavigationDrawer() {
  const { open, setOpen } = useNavDrawer();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down('sm'));

  if (isMobile) {
    return (
      <Drawer
        id="navigation-drawer"
        variant="temporary"
        anchor="left"
        open={open}
        onClose={() => setOpen(false)}
        slotProps={{ paper: { sx: { width: NAV_DRAWER_WIDTH, boxSizing: 'border-box' } } }}
      >
        <NavList expanded onItemClick={() => setOpen(false)} />
      </Drawer>
    );
  }

  const width = open ? NAV_DRAWER_WIDTH : NAV_DRAWER_WIDTH_COLLAPSED;

  return (
    <Drawer
      id="navigation-drawer"
      variant="permanent"
      anchor="left"
      sx={{
        width,
        flexShrink: 0,
        whiteSpace: 'nowrap',
        transition: theme.transitions.create('width', {
          easing: open ? theme.transitions.easing.easeOut : theme.transitions.easing.sharp,
          duration: open
            ? theme.transitions.duration.enteringScreen
            : theme.transitions.duration.leavingScreen,
        }),
        '& .MuiDrawer-paper': {
          position: 'relative',
          width,
          overflowX: 'hidden',
          height: '100%',
          boxSizing: 'border-box',
          borderRight: 1,
          borderColor: 'divider',
          transition: theme.transitions.create('width', {
            easing: open ? theme.transitions.easing.easeOut : theme.transitions.easing.sharp,
            duration: open
              ? theme.transitions.duration.enteringScreen
              : theme.transitions.duration.leavingScreen,
          }),
        },
      }}
    >
      <NavList expanded={open} />
    </Drawer>
  );
}
