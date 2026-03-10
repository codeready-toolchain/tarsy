/**
 * ScoreBadge — color-coded score chip for session scoring.
 *
 * Displays the numeric score (0-100) with color thresholds:
 *   green >= 80, yellow >= 60, red < 60.
 * Also handles non-scored states: in-progress (spinner), not scored (dash),
 * scoring failed (error icon).
 *
 * The backend `scoring_status` field returns raw session_scores status values:
 * completed, in_progress, pending, failed, timed_out, cancelled (or null when not scored).
 */

import { Chip, CircularProgress, Tooltip, Typography } from '@mui/material';
import { Error as ErrorIcon } from '@mui/icons-material';
import {
  EXECUTION_STATUS,
  FAILED_EXECUTION_STATUSES,
} from '../../constants/sessionStatus.ts';

interface ScoreBadgeProps {
  score?: number | null;
  scoringStatus?: string | null;
  size?: 'small' | 'medium';
  onClick?: () => void;
}

function getScoreColor(score: number): 'success' | 'warning' | 'error' {
  if (score >= 80) return 'success';
  if (score >= 60) return 'warning';
  return 'error';
}

const IN_PROGRESS_STATUSES = new Set<string>([
  EXECUTION_STATUS.ACTIVE,
  EXECUTION_STATUS.PENDING,
  EXECUTION_STATUS.STARTED,
]);

export function ScoreBadge({ score, scoringStatus, size = 'small', onClick }: ScoreBadgeProps) {
  const clickProps = onClick
    ? { onClick, sx: { cursor: 'pointer' } }
    : {};

  // Scored: show colored score chip
  if (score != null && scoringStatus === EXECUTION_STATUS.COMPLETED) {
    const color = getScoreColor(score);
    return (
      <Tooltip title={`Eval score: ${score} / 100`}>
        <Chip
          label={score}
          size={size}
          color={color}
          variant="filled"
          {...clickProps}
          sx={{
            fontWeight: 700,
            fontSize: size === 'small' ? '0.8rem' : '0.9rem',
            minWidth: 40,
            ...clickProps.sx,
          }}
        />
      </Tooltip>
    );
  }

  // Scoring in progress
  if (scoringStatus != null && IN_PROGRESS_STATUSES.has(scoringStatus)) {
    return (
      <Tooltip title="Scoring in progress">
        <Chip
          icon={<CircularProgress size={14} color="inherit" />}
          label="Scoring"
          size={size}
          color="info"
          variant="outlined"
          sx={{ fontWeight: 500, fontSize: '0.75rem' }}
        />
      </Tooltip>
    );
  }

  // Scoring failed / timed out
  if (scoringStatus != null && FAILED_EXECUTION_STATUSES.has(scoringStatus)) {
    return (
      <Tooltip title="Scoring failed">
        <Chip
          icon={<ErrorIcon sx={{ fontSize: 16 }} />}
          label="Score Failed"
          size={size}
          color="error"
          variant="outlined"
          {...clickProps}
          sx={{ fontWeight: 500, fontSize: '0.75rem', ...clickProps.sx }}
        />
      </Tooltip>
    );
  }

  // Not scored or unknown — match Chip height for alignment
  return (
    <Typography
      variant="body2"
      color="text.secondary"
      sx={{ height: size === 'small' ? 24 : 32, lineHeight: size === 'small' ? '24px' : '32px' }}
    >
      —
    </Typography>
  );
}
