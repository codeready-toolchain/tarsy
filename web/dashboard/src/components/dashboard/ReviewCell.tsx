import { TableCell, Tooltip, Chip } from '@mui/material';
import { ThumbsUpDown } from '@mui/icons-material';
import { qualityReviewBodySx } from './qualityGroupSx.ts';
import { getRatingConfig } from '../../constants/ratingConfig.ts';
import { isTerminalStatus, type SessionStatus } from '../../constants/sessionStatus.ts';
import type { DashboardSessionItem } from '../../types/session.ts';

const iconOnlyChipSx = {
  height: 24,
  minWidth: 24,
  '& .MuiChip-label': { px: 0, display: 'none' },
  '& .MuiChip-icon': { mx: 0 },
} as const;

interface ReviewCellProps {
  session: DashboardSessionItem;
  onReviewClick?: (session: DashboardSessionItem) => void;
}

/**
 * Shared review cell for both sessions list and triage tables.
 * Shows a colored thumb chip when reviewed, a ghost thumb on hover when not.
 * When onReviewClick is falsy the cell is read-only (no click, no hover affordance).
 * Review is only available for terminal sessions (completed, failed, cancelled, timed_out).
 */
export function ReviewCell({ session, onReviewClick }: ReviewCellProps) {
  const cfg = getRatingConfig(session.quality_rating);
  const terminal = isTerminalStatus(session.status as SessionStatus);
  const interactive = !!onReviewClick && terminal;

  return (
    <TableCell sx={qualityReviewBodySx}>
      {cfg ? (
        <Tooltip title={interactive ? `Reviewed: ${cfg.label} — click to edit` : `Reviewed: ${cfg.label}`}>
          <Chip
            icon={<cfg.icon sx={{ fontSize: '0.875rem' }} />}
            size="small"
            color={cfg.color}
            variant="outlined"
            tabIndex={interactive ? 0 : -1}
            onClick={interactive ? (e) => { e.stopPropagation(); onReviewClick(session); } : undefined}
            sx={{ ...iconOnlyChipSx, cursor: interactive ? 'pointer' : 'default' }}
          />
        </Tooltip>
      ) : interactive ? (
        <Tooltip title="Click to review">
          <Chip
            icon={<ThumbsUpDown sx={{ fontSize: '0.875rem' }} />}
            size="small"
            variant="outlined"
            className="review-hover-icon"
            tabIndex={0}
            onClick={(e) => { e.stopPropagation(); onReviewClick(session); }}
            sx={{
              ...iconOnlyChipSx,
              cursor: 'pointer',
              opacity: 0,
              transition: 'opacity 0.15s ease-in-out',
              '&:focus-visible': { opacity: 1 },
            }}
          />
        </Tooltip>
      ) : null}
    </TableCell>
  );
}
