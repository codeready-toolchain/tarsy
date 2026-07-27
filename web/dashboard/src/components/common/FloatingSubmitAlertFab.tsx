import { Fab, Tooltip } from '@mui/material';
import { NotificationAdd } from '@mui/icons-material';
import { Link as RouterLink } from 'react-router-dom';
import { ROUTES } from '../../constants/routes.ts';

/**
 * Reusable floating action button for quick alert submission access.
 */
export function FloatingSubmitAlertFab() {
  return (
    <Tooltip title="Submit Manual Alert" placement="left">
      <Fab
        component={RouterLink}
        to={ROUTES.SUBMIT_ALERT}
        color="primary"
        aria-label="submit alert"
        sx={{
          position: 'fixed',
          bottom: 24,
          right: 24,
          zIndex: 1000,
          boxShadow: 3,
          '&:hover': {
            boxShadow: 6,
            transform: 'scale(1.05)',
          },
          transition: 'all 0.2s ease-in-out',
        }}
      >
        <NotificationAdd />
      </Fab>
    </Tooltip>
  );
}
