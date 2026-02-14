import type { ReactNode } from 'react';
import { useNavigate, Link as RouterLink } from 'react-router-dom';
import AppBar from '@mui/material/AppBar';
import Toolbar from '@mui/material/Toolbar';
import Typography from '@mui/material/Typography';
import IconButton from '@mui/material/IconButton';
import Box from '@mui/material/Box';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import { useAuth } from '../../contexts/AuthContext.tsx';
import { LoginButton } from '../auth/LoginButton.tsx';
import { UserMenu } from '../auth/UserMenu.tsx';

interface SharedHeaderProps {
  title: string;
  showBackButton?: boolean;
  children?: ReactNode; // For additional controls like toggles, status indicators, etc.
}

/**
 * Shared application header with gradient AppBar, logo, optional back button,
 * and auth UI (login button or user menu).
 * Matches the old TARSy dashboard header style.
 */
export function SharedHeader({
  title,
  showBackButton = false,
  children,
}: SharedHeaderProps) {
  const navigate = useNavigate();
  const { isAuthenticated, authAvailable } = useAuth();

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
      sx={{
        borderRadius: 1,
        background: 'linear-gradient(135deg, #1976d2 0%, #1565c0 100%)',
        boxShadow: '0 4px 16px rgba(25, 118, 210, 0.3)',
        border: '1px solid rgba(255, 255, 255, 0.1)',
        mb: 2,
      }}
    >
      <Toolbar>
        {/* Back Button */}
        {showBackButton && (
          <IconButton
            edge="start"
            color="inherit"
            aria-label="back"
            onClick={handleBackClick}
            sx={{
              mr: 2,
              background: 'rgba(255, 255, 255, 0.1)',
              backdropFilter: 'blur(10px)',
              border: '1px solid rgba(255, 255, 255, 0.15)',
              borderRadius: 2,
              transition: 'all 0.2s ease',
              '&:hover': {
                background: 'rgba(255, 255, 255, 0.2)',
                transform: 'translateY(-1px)',
                boxShadow: '0 4px 12px rgba(255, 255, 255, 0.2)',
              },
            }}
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
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              width: 40,
              height: 40,
              borderRadius: 2,
              background: 'rgba(255, 255, 255, 0.1)',
              backdropFilter: 'blur(10px)',
              border: '1px solid rgba(255, 255, 255, 0.2)',
              boxShadow:
                '0 4px 12px rgba(0, 0, 0, 0.15), 0 0 20px rgba(255, 255, 255, 0.1)',
              transition: 'all 0.3s ease',
              position: 'relative',
              overflow: 'hidden',
              textDecoration: 'none',
              '&:before': {
                content: '""',
                position: 'absolute',
                top: 0,
                left: '-100%',
                width: '100%',
                height: '100%',
                background:
                  'linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.2), transparent)',
                animation: 'none',
              },
              '&:hover': {
                background: 'rgba(255, 255, 255, 0.15)',
                transform: 'translateY(-2px) scale(1.05)',
                boxShadow:
                  '0 8px 25px rgba(0, 0, 0, 0.2), 0 0 30px rgba(255, 255, 255, 0.2)',
                '&:before': {
                  animation: 'shimmer 0.6s ease-out',
                },
              },
              '&:focus-visible': {
                outline: '2px solid rgba(255, 255, 255, 0.8)',
                outlineOffset: '2px',
              },
              '@keyframes shimmer': {
                '0%': { left: '-100%' },
                '100%': { left: '100%' },
              },
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
              fontWeight: 600,
              letterSpacing: '-0.5px',
              textShadow: '0 1px 2px rgba(0, 0, 0, 0.1)',
              background:
                'linear-gradient(45deg, #ffffff 0%, rgba(255, 255, 255, 0.9) 100%)',
              WebkitBackgroundClip: 'text',
              WebkitTextFillColor: 'transparent',
              backgroundClip: 'text',
              // Fallback for browsers that don't support background-clip: text
              color: 'white',
            }}
          >
            {title}
          </Typography>
        </Box>

        {/* Additional Controls (passed as children) */}
        {children}

        {/* Authentication Elements */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, ml: 2 }}>
          {authAvailable && !isAuthenticated && <LoginButton size="medium" />}
          {authAvailable && isAuthenticated && <UserMenu />}
        </Box>
      </Toolbar>
    </AppBar>
  );
}
