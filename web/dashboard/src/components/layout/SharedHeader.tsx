import { useNavigate, Link } from 'react-router-dom';
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
  showBack?: boolean;
}

/**
 * Shared application header with gradient AppBar, logo, optional back button,
 * and auth UI (login button or user menu).
 * Matches the old TARSy dashboard header style.
 */
export function SharedHeader({ title, showBack }: SharedHeaderProps) {
  const navigate = useNavigate();
  const { isAuthenticated, authAvailable } = useAuth();

  return (
    <AppBar
      position="static"
      sx={{
        background: 'linear-gradient(135deg, #1976d2 0%, #1565c0 100%)',
      }}
    >
      <Toolbar>
        {showBack && (
          <IconButton
            edge="start"
            color="inherit"
            onClick={() => navigate(-1)}
            sx={{ mr: 1 }}
            aria-label="Go back"
          >
            <ArrowBackIcon />
          </IconButton>
        )}

        <Typography
          variant="h6"
          component={Link}
          to="/"
          sx={{
            flexGrow: 1,
            textDecoration: 'none',
            color: 'inherit',
          }}
        >
          {title}
        </Typography>

        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {authAvailable && (
            <>
              {isAuthenticated ? <UserMenu /> : <LoginButton />}
            </>
          )}
        </Box>
      </Toolbar>
    </AppBar>
  );
}
