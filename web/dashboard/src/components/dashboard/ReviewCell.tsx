import { TableCell, Tooltip, Chip } from '@mui/material';
import { ThumbsUpDown } from '@mui/icons-material';
import { qualityReviewBodySx } from './qualityGroupSx.ts';
import { RATING_CONFIG } from '../../constants/ratingConfig.ts';
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
 */
export function ReviewCell({ session, onReviewClick }: ReviewCellProps) {
  const cfg = session.quality_rating ? RATING_CONFIG[session.quality_rating] : null;

  return (
    <TableCell sx={qualityReviewBodySx}>
      {cfg ? (
        <Tooltip title={`Reviewed: ${cfg.label}`}>
          <Chip
            icon={<cfg.icon sx={{ fontSize: '0.875rem' }} />}
            size="small"
            color={cfg.color}
            variant="outlined"
            onClick={(e) => {
              e.stopPropagation();
              onReviewClick?.(session);
            }}
            sx={{ ...iconOnlyChipSx, cursor: 'pointer' }}
          />
        </Tooltip>
      ) : (
        <Tooltip title="Click to review">
          <Chip
            icon={<ThumbsUpDown sx={{ fontSize: '0.875rem' }} />}
            size="small"
            variant="outlined"
            className="review-hover-icon"
            onClick={(e) => {
              e.stopPropagation();
              onReviewClick?.(session);
            }}
            sx={{
              ...iconOnlyChipSx,
              cursor: 'pointer',
              opacity: 0,
              transition: 'opacity 0.15s ease-in-out',
            }}
          />
        </Tooltip>
      )}
    </TableCell>
  );
}
