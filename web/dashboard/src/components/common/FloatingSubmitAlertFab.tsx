import { Fab, Tooltip } from '@mui/material';
import { NotificationAdd } from '@mui/icons-material';

/**
 * Reusable floating action button for quick alert submission access.
 * Uses proper anchor-based navigation to prevent tabnabbing security issues.
 */
export function FloatingSubmitAlertFab() {
  return (
    <Tooltip title="Submit Manual Alert" placement="left">
      <Fab
        component="a"
        href="/submit-alert"
        target="_blank"
        rel="noopener noreferrer"
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
