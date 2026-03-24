import type { ReactNode } from 'react';
import { useNavigate, Link as RouterLink } from 'react-router-dom';
import AppBar from '@mui/material/AppBar';
import Toolbar from '@mui/material/Toolbar';
import Typography from '@mui/material/Typography';
import IconButton from '@mui/material/IconButton';
import Box from '@mui/material/Box';
import Tooltip from '@mui/material/Tooltip';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import DarkModeIcon from '@mui/icons-material/DarkMode';
import LightModeIcon from '@mui/icons-material/LightMode';
import { useAuth } from '../../contexts/AuthContext.tsx';
import { useColorScheme } from '@mui/material/styles';
import { LoginButton } from '../auth/LoginButton.tsx';
import { UserMenu } from '../auth/UserMenu.tsx';
import { appBarSx, glassIconButtonSx, logoBoxSx, titleSx, themeToggleSx } from '../../theme/headerStyles';

interface SharedHeaderProps {
  title: string;
  showBackButton?: boolean;
  children?: ReactNode; // For additional controls like toggles, status indicators, etc.
}

/**
 * Shared application header with gradient AppBar, logo, optional back button,
 * and auth UI (login button or user menu).
 */
export function SharedHeader({
  title,
  showBackButton = false,
  children,
}: SharedHeaderProps) {
  const navigate = useNavigate();
  const { isAuthenticated, authAvailable } = useAuth();
  const { mode, setMode } = useColorScheme();
  const toggleColorMode = () => setMode(mode === 'dark' ? 'light' : 'dark');

  const handleBackClick = () => {
    // Smart back navigation:
    // - If there's history (same-tab navigation), go back
    // - If no history (opened in new tab), go to home page
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
      sx={(theme) => ({
        ...appBarSx(theme),
        mb: 2,
      })}
    >
      <Toolbar>
        {/* Back Button */}
        {showBackButton && (
          <IconButton
            edge="start"
            color="inherit"
            aria-label="back"
            onClick={handleBackClick}
            sx={{ mr: 2, ...glassIconButtonSx }}
          >
            <ArrowBackIcon />
          </IconButton>
        )}

        {/* Logo and Title */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, flexGrow: 1 }}>
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
          <Typography
            variant="h5"
            component="div"
            sx={{
              ...titleSx,
            }}
          >
            {title}
          </Typography>
        </Box>

        {/* Additional Controls (passed as children) */}
        {children}

        {/* Dark / Light mode toggle */}
        <Tooltip title={mode === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}>
          <IconButton
            size="small"
            onClick={toggleColorMode}
            sx={{ ml: 1, ...themeToggleSx }}
          >
            {mode === 'dark' ? <LightModeIcon fontSize="small" /> : <DarkModeIcon fontSize="small" />}
          </IconButton>
        </Tooltip>

        {/* Authentication Elements */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, ml: 2 }}>
          {authAvailable && !isAuthenticated && <LoginButton size="medium" />}
          {authAvailable && isAuthenticated && <UserMenu />}
        </Box>
      </Toolbar>
    </AppBar>
  );
}
