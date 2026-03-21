import type { SxProps, Theme } from '@mui/material';
import { TableCell, TableSortLabel } from '@mui/material';
import { qualityEvalScoreHeaderSx, qualityReviewHeaderSx } from './qualityGroupSx.ts';

interface QualityGroupHeadersProps {
  sortField?: string;
  sortDirection?: 'asc' | 'desc';
  onSortChange?: (field: string) => void;
}

const staticLabelSx: SxProps<Theme> = {
  cursor: 'default',
  '&:hover': { color: 'inherit' },
  '& .MuiTableSortLabel-icon': { opacity: '0 !important' },
};

/**
 * Shared header cells for the Eval Score + Review quality group.
 * Used in both sessions list and triage tables to guarantee identical layout.
 *
 * Always renders TableSortLabel (with a hidden-but-space-occupying icon) so
 * the internal spacing is identical regardless of whether sorting is enabled.
 */
export function QualityGroupHeaders({ sortField, sortDirection, onSortChange }: QualityGroupHeadersProps) {
  const sortable = !!onSortChange;

  return (
    <>
      <TableCell sx={{ fontWeight: 600, ...qualityEvalScoreHeaderSx }}>
        <TableSortLabel
          active={sortable && sortField === 'score'}
          direction={sortField === 'score' ? sortDirection : 'desc'}
          onClick={sortable ? () => onSortChange('score') : undefined}
          sx={!sortable ? staticLabelSx : undefined}
        >
          Eval Score
        </TableSortLabel>
      </TableCell>
      <TableCell sx={{ fontWeight: 600, ...qualityReviewHeaderSx }}>
        <TableSortLabel
          active={sortable && sortField === 'quality_rating'}
          direction={sortField === 'quality_rating' ? sortDirection : 'desc'}
          onClick={sortable ? () => onSortChange('quality_rating') : undefined}
          sx={!sortable ? staticLabelSx : undefined}
        >
          Review
        </TableSortLabel>
      </TableCell>
    </>
  );
}
