/**
 * EstimatedCostDisplay — sibling of TokenUsageDisplay for estimated USD cost.
 *
 * Soft "Est. $X" with tooltip caveats; warning when completeness is partial.
 * Hidden when estimation is disabled, fields are absent, or completeness is none.
 */

import { memo } from 'react';
import { Box, Typography, Tooltip } from '@mui/material';
import WarningAmberRounded from '@mui/icons-material/WarningAmberRounded';
import { formatEstimatedCostUsd } from '../../utils/format';
import type { CostCompleteness, ExecutionOverview } from '../../types/session';

/** Client-side cost rollup for parallel / sub-agent aggregate summaries. */
export function rollupExecutionCost(overviews: ExecutionOverview[]): {
  estimatedCostUsd?: number;
  costCompleteness?: CostCompleteness;
} {
  const withCost = overviews.filter((eo) => eo.cost_completeness != null);
  if (withCost.length === 0) return {};

  const estimatedCostUsd = withCost.reduce((sum, eo) => sum + (eo.estimated_cost_usd ?? 0), 0);
  const statuses = new Set(withCost.map((eo) => eo.cost_completeness));
  let costCompleteness: CostCompleteness = 'complete';
  if (statuses.has('partial') || (statuses.has('complete') && statuses.has('none'))) {
    costCompleteness = 'partial';
  } else if (statuses.has('none') && !statuses.has('complete')) {
    costCompleteness = 'none';
  }
  return { estimatedCostUsd, costCompleteness };
}

export interface EstimatedCostDisplayProps {
  estimatedCostUsd?: number | null;
  costCompleteness?: CostCompleteness | null;
  /** When false, render nothing. Defaults to true when cost fields are passed. */
  enabled?: boolean;
  size?: 'small' | 'medium';
  variant?: 'inline' | 'labeled';
}

const CAVEAT_COMPLETE =
  'Estimated list-price cost — not invoice truth. Rates are resolved at write time.';
const CAVEAT_PARTIAL =
  'Partial estimate: some token-bearing interactions were unpriced. Total may undercount.';

function EstimatedCostDisplay({
  estimatedCostUsd,
  costCompleteness,
  enabled = true,
  size = 'medium',
  variant = 'inline',
}: EstimatedCostDisplayProps) {
  if (
    !enabled ||
    estimatedCostUsd == null ||
    costCompleteness == null ||
    costCompleteness === 'none'
  ) {
    return null;
  }

  const label = `Est. ${formatEstimatedCostUsd(estimatedCostUsd)}`;
  const isPartial = costCompleteness === 'partial';
  const tooltip = isPartial ? CAVEAT_PARTIAL : CAVEAT_COMPLETE;
  const fontSize = size === 'small' ? '0.75rem' : '0.875rem';

  const content = (
    <Box
      component="span"
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 0.25,
        cursor: 'help',
      }}
    >
      {variant === 'labeled' && (
        <Typography
          component="span"
          variant="caption"
          color="text.secondary"
          sx={{ fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.5, mr: 0.5 }}
        >
          Cost
        </Typography>
      )}
      <Typography
        component="span"
        variant="body2"
        color={isPartial ? 'warning.main' : 'text.secondary'}
        sx={{ fontWeight: 600, fontSize, whiteSpace: 'nowrap' }}
      >
        {label}
      </Typography>
      {isPartial && (
        <WarningAmberRounded
          sx={{ fontSize: size === 'small' ? '0.9rem' : '1rem', color: 'warning.main' }}
          aria-label="Incomplete cost estimate"
        />
      )}
    </Box>
  );

  return (
    <Tooltip title={tooltip} arrow>
      {content}
    </Tooltip>
  );
}

export default memo(EstimatedCostDisplay);
