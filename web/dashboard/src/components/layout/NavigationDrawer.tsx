import { Link as RouterLink, useLocation } from 'react-router-dom';
import Drawer from '@mui/material/Drawer';
import List from '@mui/material/List';
import ListItem from '@mui/material/ListItem';
import ListItemButton from '@mui/material/ListItemButton';
import ListItemIcon from '@mui/material/ListItemIcon';
import ListItemText from '@mui/material/ListItemText';
import HomeIcon from '@mui/icons-material/Home';
import FactCheckIcon from '@mui/icons-material/FactCheck';
import SendIcon from '@mui/icons-material/Send';
import BarChartIcon from '@mui/icons-material/BarChart';
import DnsIcon from '@mui/icons-material/Dns';
import { ROUTES } from '../../constants/routes.ts';
import { useNavDrawer, NAV_DRAWER_WIDTH } from '../../contexts/NavDrawerContext.tsx';

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
 * Persistent left navigation drawer, docked below the global header as a
 * flex sibling of the main content — pushes it when open. Open/closed state
 * is owned by NavDrawerContext (remembered across routes).
 */
export function NavigationDrawer() {
  const { open } = useNavDrawer();
  const { pathname } = useLocation();

  return (
    <Drawer
      id="navigation-drawer"
      variant="persistent"
      anchor="left"
      open={open}
      sx={{
        width: NAV_DRAWER_WIDTH,
        flexShrink: 0,
        '& .MuiDrawer-paper': {
          position: 'relative',
          width: NAV_DRAWER_WIDTH,
          height: '100%',
          boxSizing: 'border-box',
          borderRight: 1,
          borderColor: 'divider',
        },
      }}
    >
      <List sx={{ py: 1 }}>
        {NAV_ITEMS.map((item) => {
          const selected = item.match(pathname);
          return (
            <ListItem key={item.to} disablePadding>
              <ListItemButton
                component={RouterLink}
                to={item.to}
                selected={selected}
              >
                <ListItemIcon sx={{ minWidth: 40 }}>{item.icon}</ListItemIcon>
                <ListItemText primary={item.label} />
              </ListItemButton>
            </ListItem>
          );
        })}
      </List>
    </Drawer>
  );
}
