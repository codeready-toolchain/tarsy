import { Link as RouterLink, useLocation } from 'react-router-dom';
import Drawer from '@mui/material/Drawer';
import List from '@mui/material/List';
import ListItem from '@mui/material/ListItem';
import ListItemButton from '@mui/material/ListItemButton';
import ListItemIcon from '@mui/material/ListItemIcon';
import ListItemText from '@mui/material/ListItemText';
import Tooltip from '@mui/material/Tooltip';
import { useTheme } from '@mui/material/styles';
import HomeIcon from '@mui/icons-material/Home';
import FactCheckIcon from '@mui/icons-material/FactCheck';
import SendIcon from '@mui/icons-material/Send';
import BarChartIcon from '@mui/icons-material/BarChart';
import DnsIcon from '@mui/icons-material/Dns';
import { ROUTES } from '../../constants/routes.ts';
import {
  useNavDrawer,
  NAV_DRAWER_WIDTH,
  NAV_DRAWER_WIDTH_COLLAPSED,
} from '../../contexts/NavDrawerContext.tsx';

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

/**
 * Left navigation drawer, docked below the global header as a flex sibling
 * of the main content. Always visible — collapsed to an icon-only rail by
 * default, expanding in place to show labels too. Open/collapsed state is
 * owned by NavDrawerContext (remembered across routes).
 */
export function NavigationDrawer() {
  const { open } = useNavDrawer();
  const { pathname } = useLocation();
  const theme = useTheme();
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
      <List sx={{ py: 1 }}>
        {NAV_ITEMS.map((item) => {
          const selected = item.match(pathname);
          const button = (
            <ListItemButton
              component={RouterLink}
              to={item.to}
              selected={selected}
              sx={{
                minHeight: 44,
                justifyContent: open ? 'flex-start' : 'center',
                px: 2.5,
              }}
            >
              <ListItemIcon sx={{ minWidth: 0, mr: open ? 2 : 0, justifyContent: 'center' }}>
                {item.icon}
              </ListItemIcon>
              <ListItemText
                primary={item.label}
                sx={{
                  opacity: open ? 1 : 0,
                  transition: theme.transitions.create('opacity', {
                    duration: theme.transitions.duration.shortest,
                  }),
                }}
              />
            </ListItemButton>
          );

          return (
            <ListItem key={item.to} disablePadding sx={{ display: 'block' }}>
              {open ? button : (
                <Tooltip title={item.label} placement="right">
                  {button}
                </Tooltip>
              )}
            </ListItem>
          );
        })}
      </List>
    </Drawer>
  );
}
