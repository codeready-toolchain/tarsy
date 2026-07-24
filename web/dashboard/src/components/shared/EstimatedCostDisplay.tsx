/**
 * EstimatedCostDisplay — sibling of TokenUsageDisplay for estimated USD cost.
 *
 * Value-first "$X" (no extra "approximate" glyph — the "$" already reads as
 * money, the surrounding label/header/caption already says "Cost"/"Est.",
 * and the tooltip carries the full caveat; a per-value glyph was redundant
 * and, at small sizes, easy to misread). Matches TokenUsageDisplay's
 * number-then-muted-label grammar for the 'labeled' variant. Complete
 * estimates get a dedicated accent color (not plain gray text) so cost holds
 * its own next to the colorful token figures it's usually paired with.
 * `size="medium"` (default) matches SessionHeader's bespoke body2 token
 * sizing; `size="small"` matches TokenUsageDisplay's own small/labeled
 * sizing used in tables and parallel/sub-agent cards.
 * Tooltip carries the full estimate caveat; warning color + icon when
 * completeness is partial. Hidden when estimation is disabled, fields are
 * absent, or completeness is none.
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

  const isPartial = costCompleteness === 'partial';
  const tooltip = isPartial ? CAVEAT_PARTIAL : CAVEAT_COMPLETE;
  // 'small' matches TokenUsageDisplay's own small/labeled sizing (dense
  // contexts: tables, parallel/sub-agent cards). 'medium' matches
  // SessionHeader's bespoke body2/caption token sizing (the hero display).
  const fs = size === 'small' ? '0.7rem' : '0.875rem';
  const labelFs = size === 'small' ? '0.65rem' : '0.75rem';

  const content = (
    <Box
      component="span"
      sx={{
        display: 'inline-flex',
        alignItems: 'baseline',
        gap: 0.5,
        cursor: 'help',
      }}
    >
      <Typography
        component="span"
        variant="caption"
        color={isPartial ? 'warning.main' : 'secondary.main'}
        sx={{ fontWeight: 700, fontSize: fs, whiteSpace: 'nowrap' }}
      >
        {formatEstimatedCostUsd(estimatedCostUsd)}
      </Typography>
      {variant === 'labeled' && (
        <Typography component="span" variant="caption" color="text.disabled" sx={{ fontSize: labelFs }}>
          cost
        </Typography>
      )}
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
