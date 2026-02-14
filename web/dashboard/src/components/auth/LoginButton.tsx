import Button from '@mui/material/Button';
import LoginIcon from '@mui/icons-material/Login';
import { authService } from '../../services/auth.ts';

/**
 * Login button that redirects to oauth2-proxy sign-in page.
 */
export function LoginButton() {
  return (
    <Button
      color="inherit"
      startIcon={<LoginIcon />}
      onClick={() => authService.redirectToLogin()}
      size="small"
    >
      Sign In
    </Button>
  );
}
