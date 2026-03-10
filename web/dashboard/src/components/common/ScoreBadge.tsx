/**
 * ScoreBadge — color-coded score indicator for session scoring.
 *
 * Displays the numeric score (0-100) with color thresholds:
 *   green >= 80, yellow >= 60, red < 60.
 * Also handles non-scored states: in-progress (spinner), not scored (dash),
 * scoring failed (error icon).
 *
 * Two visual variants:
 *   - "chip"  (default) — compact MUI Chip, suited for table rows.
 *   - "pill"  — tinted outline pill matching the Session Summary stat badges.
 *
 * The backend `scoring_status` field returns raw session_scores status values:
 * completed, in_progress, pending, failed, timed_out, cancelled (or null when not scored).
 */

import { Box, Chip, CircularProgress, Tooltip, Typography, alpha } from '@mui/material';
import { Error as ErrorIcon } from '@mui/icons-material';
import {
  EXECUTION_STATUS,
  FAILED_EXECUTION_STATUSES,
} from '../../constants/sessionStatus.ts';

type ScoreColor = 'success' | 'warning' | 'error';

interface ScoreBadgeProps {
  score?: number | null;
  scoringStatus?: string | null;
  size?: 'small' | 'medium';
  /** "chip" = compact chip (tables), "pill" = tinted outline pill (header stats row). */
  variant?: 'chip' | 'pill';
  /** Show the text label next to the value in pill variant (default true). */
  showLabel?: boolean;
  onClick?: () => void;
}

function getScoreColor(score: number): ScoreColor {
  if (score >= 80) return 'success';
  if (score >= 60) return 'warning';
  return 'error';
}

const IN_PROGRESS_STATUSES = new Set<string>([
  EXECUTION_STATUS.ACTIVE,
  EXECUTION_STATUS.PENDING,
  EXECUTION_STATUS.STARTED,
]);

function PillBadge({
  color,
  icon,
  value,
  label,
  tooltip,
  onClick,
}: {
  color: ScoreColor | 'info';
  icon?: React.ReactNode;
  value: React.ReactNode;
  label?: string;
  tooltip: string;
  onClick?: () => void;
}) {
  const paletteKey = color === 'info' ? 'info' : color;
  return (
    <Tooltip title={tooltip}>
      <Box
        onClick={onClick}
        sx={(theme) => ({
          display: 'inline-flex',
          alignItems: 'center',
          gap: 0.5,
          px: 1,
          py: 0.5,
          backgroundColor: alpha(theme.palette[paletteKey].main, 0.08),
          borderRadius: '16px',
          border: '1px solid',
          borderColor: alpha(theme.palette[paletteKey].main, 0.25),
          ...(onClick && {
            cursor: 'pointer',
            transition: 'all 0.2s ease-in-out',
            '&:hover': {
              backgroundColor: alpha(theme.palette[paletteKey].main, 0.16),
              borderColor: alpha(theme.palette[paletteKey].main, 0.5),
            },
          }),
        })}
      >
        {icon}
        <Typography
          variant="body2"
          sx={{ fontWeight: 600, color: `${paletteKey}.main`, minWidth: '3ch', textAlign: 'center' }}
        >
          {value}
        </Typography>
        {label && (
          <Typography variant="caption" sx={{ color: `${paletteKey}.main` }}>
            {label}
          </Typography>
        )}
      </Box>
    </Tooltip>
  );
}

export function ScoreBadge({ score, scoringStatus, size = 'small', variant = 'chip', showLabel = true, onClick }: ScoreBadgeProps) {
  const clickProps = onClick
    ? { onClick, sx: { cursor: 'pointer' } }
    : {};

  // --- Scored ---
  if (score != null && scoringStatus === EXECUTION_STATUS.COMPLETED) {
    const color = getScoreColor(score);

    if (variant === 'pill') {
      return (
        <PillBadge
          color={color}
          value={score}
          label={showLabel ? 'eval' : undefined}
          tooltip={`Eval score: ${score} / 100`}
          onClick={onClick}
        />
      );
    }

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

  // --- Scoring in progress ---
  if (scoringStatus != null && IN_PROGRESS_STATUSES.has(scoringStatus)) {
    if (variant === 'pill') {
      return (
        <PillBadge
          color="info"
          icon={<CircularProgress size={12} color="inherit" />}
          value=""
          label="scoring…"
          tooltip="Scoring in progress"
          onClick={onClick}
        />
      );
    }

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

  // --- Scoring failed / timed out ---
  if (scoringStatus != null && FAILED_EXECUTION_STATUSES.has(scoringStatus)) {
    if (variant === 'pill') {
      return (
        <PillBadge
          color="error"
          icon={<ErrorIcon sx={{ fontSize: 14 }} />}
          value=""
          label="score failed"
          tooltip="Scoring failed"
          onClick={onClick}
        />
      );
    }

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
