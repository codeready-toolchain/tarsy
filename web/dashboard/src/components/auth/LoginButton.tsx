import IconButton from '@mui/material/IconButton';
import Tooltip from '@mui/material/Tooltip';
import LoginIcon from '@mui/icons-material/Login';
import { authService } from '../../services/auth.ts';

interface LoginButtonProps {
  size?: 'small' | 'medium' | 'large';
}

/**
 * Icon-only login button that redirects to oauth2-proxy sign-in page.
 * White icon with tooltip, glass-style hover.
 */
export function LoginButton({ size = 'medium' }: LoginButtonProps) {
  return (
    <Tooltip title="Login with GitHub">
      <IconButton
        size={size}
        onClick={() => authService.redirectToLogin()}
        sx={{
          color: 'white',
          '&:hover': {
            backgroundColor: 'rgba(255, 255, 255, 0.1)',
          },
        }}
      >
        <LoginIcon />
      </IconButton>
    </Tooltip>
  );
}
