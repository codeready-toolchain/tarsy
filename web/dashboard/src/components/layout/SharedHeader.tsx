import { useNavigate, Link as RouterLink } from 'react-router-dom';
import AppBar from '@mui/material/AppBar';
import Toolbar from '@mui/material/Toolbar';
import Typography from '@mui/material/Typography';
import IconButton from '@mui/material/IconButton';
import Box from '@mui/material/Box';
import Tooltip from '@mui/material/Tooltip';
import MenuIcon from '@mui/icons-material/Menu';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import DarkModeIcon from '@mui/icons-material/DarkMode';
import LightModeIcon from '@mui/icons-material/LightMode';
import { useAuth } from '../../contexts/AuthContext.tsx';
import { useNavDrawer } from '../../contexts/NavDrawerContext.tsx';
import { usePageHeaderState } from '../../contexts/PageHeaderContext.tsx';
import { useColorScheme } from '@mui/material/styles';
import { LoginButton } from '../auth/LoginButton.tsx';
import { UserMenu } from '../auth/UserMenu.tsx';
import { appBarSx, glassIconButtonSx, logoBoxSx, titleSx, themeToggleSx } from '../../theme/headerStyles';

/**
 * Single global app header, owned by AppLayout. Spans the full viewport width
 * above both the nav drawer and the main content — reads its title/back-button/
 * actions from PageHeaderContext, published by whichever page is active.
 */
export function SharedHeader() {
  const navigate = useNavigate();
  const { isAuthenticated, authAvailable } = useAuth();
  const { open: navOpen, toggle: toggleNav } = useNavDrawer();
  const { title, showBackButton, actions } = usePageHeaderState();
  const { mode, setMode } = useColorScheme();
  const toggleColorMode = () => setMode(mode === 'light' ? 'dark' : 'light');

  const handleBackClick = () => {
    if (window.history.length > 1) {
      navigate(-1);
    } else {
      navigate('/');
    }
  };

  return (
    <AppBar
      position="static"
      elevation={0}
      sx={(theme) => ({ ...appBarSx(theme) })}
    >
      <Toolbar>
        {/* Uniform 8px gap across hamburger/back/home — they're all icon-sized nav
            controls in the same row, so a consistent rhythm reads as one group.
            Not a conditional margin on the hamburger itself — otherwise the
            hamburger's own margin would change (and animate via glassIconButtonSx's
            transition) whenever the back button mounts/unmounts, sliding everything after it. */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <IconButton
            id="navigation-menu-button"
            edge="start"
            color="inherit"
            aria-label={navOpen ? 'Collapse navigation menu' : 'Expand navigation menu'}
            aria-controls="navigation-drawer"
            aria-expanded={navOpen}
            onClick={toggleNav}
            sx={glassIconButtonSx}
          >
            <MenuIcon />
          </IconButton>

          {showBackButton && (
            <IconButton
              color="inherit"
              aria-label="back"
              onClick={handleBackClick}
              sx={glassIconButtonSx}
            >
              <ArrowBackIcon />
            </IconButton>
          )}

          <Box
            component={RouterLink}
            to="/"
            aria-label="Home"
            sx={{
              ...logoBoxSx,
            }}
          >
            <img
              src="/tarsy-logo.png"
              alt="TARSy logo"
              style={{
                height: '28px',
                width: 'auto',
                borderRadius: '3px',
                filter: 'drop-shadow(0 2px 4px rgba(0, 0, 0, 0.1))',
              }}
            />
          </Box>
        </Box>

        <Typography
          variant="h5"
          component="div"
          sx={{
            ml: 2,
            flexGrow: 1,
            ...titleSx,
          }}
        >
          {title}
        </Typography>

        {actions}

        <Tooltip title={mode === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}>
          <IconButton
            size="small"
            onClick={toggleColorMode}
            aria-label={mode === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
            sx={{ ml: 1, ...themeToggleSx }}
          >
            {mode === 'dark' ? <LightModeIcon fontSize="small" /> : <DarkModeIcon fontSize="small" />}
          </IconButton>
        </Tooltip>

        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, ml: 2 }}>
          {authAvailable && !isAuthenticated && <LoginButton size="medium" />}
          {authAvailable && isAuthenticated && <UserMenu />}
        </Box>
      </Toolbar>
    </AppBar>
  );
}
