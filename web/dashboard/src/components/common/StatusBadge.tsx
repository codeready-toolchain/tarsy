/**
 * StatusBadge — MUI Chip showing session status with icon and color.
 *
 * Ported from old dashboard's StatusBadge.tsx. Adapted for new TARSy statuses
 * (no paused/canceling; uses cancelling).
 */

import { Chip, type ChipProps } from '@mui/material';
import {
  CheckCircle,
  Error as ErrorIcon,
  Schedule,
  Refresh,
  HourglassEmpty,
  Cancel,
  AccessAlarm,
} from '@mui/icons-material';
import { SESSION_STATUS, type SessionStatus } from '../../constants/sessionStatus.ts';

interface StatusBadgeProps {
  status: string;
  size?: 'small' | 'medium';
}

interface StatusConfig {
  color: ChipProps['color'];
  icon: React.ReactElement;
  label: string;
}

function getStatusConfig(status: string): StatusConfig {
  switch (status) {
    case SESSION_STATUS.PENDING:
      return { color: 'warning', icon: <Schedule sx={{ fontSize: 16 }} />, label: 'Pending' };
    case SESSION_STATUS.IN_PROGRESS:
      return { color: 'info', icon: <Refresh sx={{ fontSize: 16 }} />, label: 'In Progress' };
    case SESSION_STATUS.CANCELLING:
      return {
        color: 'warning',
        icon: <HourglassEmpty sx={{ fontSize: 16 }} />,
        label: 'Canceling',
      };
    case SESSION_STATUS.COMPLETED:
      return { color: 'success', icon: <CheckCircle sx={{ fontSize: 16 }} />, label: 'Completed' };
    case SESSION_STATUS.FAILED:
      return { color: 'error', icon: <ErrorIcon sx={{ fontSize: 16 }} />, label: 'Failed' };
    case SESSION_STATUS.CANCELLED:
      return { color: 'default', icon: <Cancel sx={{ fontSize: 16 }} />, label: 'Cancelled' };
    case SESSION_STATUS.TIMED_OUT:
      return { color: 'error', icon: <AccessAlarm sx={{ fontSize: 16 }} />, label: 'Timed Out' };
    default:
      return { color: 'default', icon: <Schedule sx={{ fontSize: 16 }} />, label: status };
  }
}

export function StatusBadge({ status, size = 'small' }: StatusBadgeProps) {
  const { color, icon, label } = getStatusConfig(status as SessionStatus);

  // Base styling shared across all statuses (ported from old dashboard)
  const baseSx = {
    fontWeight: 500,
    // Disable unnecessary hover transitions/animations (prevents visual jitter)
    transition: 'none',
    transform: 'none',
    '&.MuiChip-root': { animation: 'none' },
    // Disable ripple effect
    '& .MuiTouchRipple-root': { display: 'none' },
    '& .MuiChip-icon': { marginLeft: '4px' },
    // Accessibility: keyboard focus indicator
    '&:focus-visible': {
      outline: '2px solid',
      outlineColor: 'primary.main',
      outlineOffset: '2px',
      boxShadow: '0 0 0 4px rgba(25, 118, 210, 0.2)',
    },
  };

  // Custom override for cancelled status
  const cancelledSx =
    status === SESSION_STATUS.CANCELLED
      ? {
          fontWeight: 600,
          backgroundColor: 'rgba(0, 0, 0, 0.7)',
          color: 'white',
          border: '1px solid rgba(0, 0, 0, 0.8)',
          '& .MuiChip-icon': { marginLeft: '4px', color: 'white' },
        }
      : {};

  return (
    <Chip
      size={size}
      color={color}
      icon={icon}
      label={label}
      variant="filled"
      sx={{ ...baseSx, ...cancelledSx }}
    />
  );
}
