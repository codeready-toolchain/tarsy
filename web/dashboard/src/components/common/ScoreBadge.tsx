/**
 * ScoreBadge — color-coded score chip for session scoring.
 *
 * Displays the numeric score (0-100) with color thresholds:
 *   green >= 80, yellow >= 60, red < 60.
 * Also handles non-scored states: in-progress (spinner), not scored (dash),
 * scoring failed (error icon).
 */

import { Chip, CircularProgress, Tooltip } from '@mui/material';
import { Error as ErrorIcon } from '@mui/icons-material';

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

function getStatusTooltip(scoringStatus: string | null | undefined): string {
  switch (scoringStatus) {
    case 'scoring_in_progress':
      return 'Scoring in progress';
    case 'scoring_failed':
      return 'Scoring failed';
    case 'not_scored':
      return 'Not scored';
    default:
      return '';
  }
}

export function ScoreBadge({ score, scoringStatus, size = 'small', onClick }: ScoreBadgeProps) {
  const clickProps = onClick
    ? { onClick, sx: { cursor: 'pointer' } }
    : {};

  if (score != null && (scoringStatus === 'scored' || scoringStatus == null)) {
    const color = getScoreColor(score);
    return (
      <Tooltip title={`Quality score: ${score}/100`}>
        <Chip
          label={`${score}/100`}
          size={size}
          color={color}
          variant="filled"
          {...clickProps}
          sx={{
            fontWeight: 600,
            fontSize: size === 'small' ? '0.75rem' : '0.875rem',
            ...clickProps.sx,
          }}
        />
      </Tooltip>
    );
  }

  if (scoringStatus === 'scoring_in_progress') {
    return (
      <Tooltip title={getStatusTooltip(scoringStatus)}>
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

  if (scoringStatus === 'scoring_failed') {
    return (
      <Tooltip title={getStatusTooltip(scoringStatus)}>
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

  // not_scored or unknown — show dash
  return null;
}
