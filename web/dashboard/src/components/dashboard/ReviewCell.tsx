import { TableCell, Tooltip, Chip } from '@mui/material';
import { ThumbUp, ThumbsUpDown, ThumbDown } from '@mui/icons-material';
import { qualityReviewBodySx } from './qualityGroupSx.ts';
import { QUALITY_RATING } from '../../types/api.ts';
import type { DashboardSessionItem } from '../../types/session.ts';

const iconOnlyChipSx = {
  height: 24,
  minWidth: 24,
  '& .MuiChip-label': { px: 0, display: 'none' },
  '& .MuiChip-icon': { mx: 0 },
} as const;

const RATING_CHIP_MAP: Record<string, {
  icon: React.ReactElement;
  chipColor: 'success' | 'warning' | 'error';
  label: string;
}> = {
  [QUALITY_RATING.ACCURATE]: { icon: <ThumbUp sx={{ fontSize: '0.875rem' }} />, chipColor: 'success', label: 'Accurate' },
  [QUALITY_RATING.PARTIALLY_ACCURATE]: { icon: <ThumbsUpDown sx={{ fontSize: '0.875rem' }} />, chipColor: 'warning', label: 'Partially Accurate' },
  [QUALITY_RATING.INACCURATE]: { icon: <ThumbDown sx={{ fontSize: '0.875rem' }} />, chipColor: 'error', label: 'Inaccurate' },
};

interface ReviewCellProps {
  session: DashboardSessionItem;
  onReviewClick?: (session: DashboardSessionItem) => void;
}

/**
 * Shared review cell for both sessions list and triage tables.
 * Shows a colored thumb chip when reviewed, a ghost thumb on hover when not.
 */
export function ReviewCell({ session, onReviewClick }: ReviewCellProps) {
  const rating = session.quality_rating ? RATING_CHIP_MAP[session.quality_rating] : null;

  return (
    <TableCell sx={qualityReviewBodySx}>
      {rating ? (
        <Tooltip title={`Reviewed: ${rating.label}`}>
          <Chip
            icon={rating.icon}
            size="small"
            color={rating.chipColor}
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
