import { useState, useEffect, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import { formatDurationMs } from '../../utils/format';
import { SESSION_STATUS } from '../../constants/sessionStatus';

interface ProgressIndicatorProps {
  /** Session status string */
  status: string;
  /** ISO 8601 timestamp for when the session started */
  startedAt?: string | null;
  /** Final duration in milliseconds (from backend, available on terminal statuses) */
  durationMs?: number | null;
  /** Whether to display the duration text */
  showDuration?: boolean;
}

/**
 * ProgressIndicator — self-contained component that manages its own
 * live-ticking timer for active sessions and displays a formatted duration.
 *
 * For active sessions: shows a ticking duration, colored by status.
 * For terminal sessions: shows the final duration text only, colored by status.
 */
export default function ProgressIndicator({
  status,
  startedAt,
  durationMs,
  showDuration = true,
}: ProgressIndicatorProps) {
  const isActive =
    status === SESSION_STATUS.IN_PROGRESS ||
    status === SESSION_STATUS.CANCELLING ||
    status === SESSION_STATUS.PENDING;

  const computeLive = useCallback((): number | null => {
    // Prefer final backend duration when available
    if (durationMs != null) return durationMs;
    if (!startedAt) return null;
    return Math.max(0, Date.now() - new Date(startedAt).getTime());
  }, [durationMs, startedAt]);

  const [liveDurationMs, setLiveDurationMs] = useState<number | null>(computeLive);

  useEffect(() => {
    // Always sync immediately (covers status transitions & prop changes)
    setLiveDurationMs(computeLive());

    if (!isActive || durationMs != null) return;

    const interval = setInterval(() => {
      setLiveDurationMs(computeLive());
    }, 1000);

    return () => clearInterval(interval);
  }, [isActive, durationMs, computeLive]);

  // --- Active: ticking duration (no progress bar) ---
  if (isActive) {
    const color =
      status === SESSION_STATUS.CANCELLING
        ? 'warning.main'
        : status === SESSION_STATUS.PENDING
          ? 'warning.main'
          : 'primary.main';

    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, width: '100%' }}>
        <Typography
          variant="caption"
          sx={{
            fontWeight: 600,
            color,
            textTransform: 'uppercase',
            letterSpacing: 0.5,
            textAlign: 'right',
          }}
        >
          Duration
        </Typography>
        {showDuration && liveDurationMs != null && (
          <Typography
            sx={{
              fontSize: '1.4rem',
              fontWeight: 800,
              color,
              textAlign: 'right',
            }}
          >
            {formatDurationMs(liveDurationMs)}
          </Typography>
        )}
      </Box>
    );
  }

  // --- Terminal: duration text only, colored by status ---
  const color =
    status === SESSION_STATUS.COMPLETED
      ? 'success.main'
      : status === SESSION_STATUS.FAILED || status === SESSION_STATUS.TIMED_OUT
        ? 'error.main'
        : status === SESSION_STATUS.CANCELLED
          ? 'text.disabled'
          : 'text.secondary';

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, width: '100%' }}>
      <Typography
        variant="caption"
        sx={{
          fontWeight: 600,
          color,
          textTransform: 'uppercase',
          letterSpacing: 0.5,
          textAlign: 'right',
        }}
      >
        Duration
      </Typography>
      {showDuration && liveDurationMs != null && (
        <Typography
          sx={{
            fontSize: '1.4rem',
            fontWeight: 800,
            color,
            textAlign: 'right',
          }}
        >
          {formatDurationMs(liveDurationMs)}
        </Typography>
      )}
    </Box>
  );
}
