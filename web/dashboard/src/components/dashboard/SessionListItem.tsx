/**
 * SessionListItem — single row in the historical sessions table.
 *
 * Ported from old dashboard's AlertListItem.tsx.
 * Adapted for new backend types: `id` instead of `session_id`,
 * RFC3339 timestamps, `total_tokens` instead of `session_total_tokens`.
 */

import {
  TableRow,
  TableCell,
  Typography,
  Tooltip,
  Chip,
  Box,
} from '@mui/material';
import {
  SmsOutlined as ChatIcon,
  CallSplit,
  FindInPage,
  Hub,
  SwapHoriz,
  BuildOutlined,
} from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { SessionLabelChips } from '../common/SessionLabelChips.tsx';
import { SummaryTooltip } from './SummaryTooltip.tsx';
import { ScoreCell } from './ScoreCell.tsx';
import { ReviewCell } from './ReviewCell.tsx';
import { qualityEvalScoreBodySx } from './qualityGroupSx.ts';
import { OpenNewTabButton } from './OpenNewTabButton.tsx';
import { highlightSearchTermNodes } from '../../utils/search.ts';
import { formatTimestamp, formatDurationMs, formatCancelAttribution, formatTokens } from '../../utils/format.ts';
import TokenUsageDisplay from '../shared/TokenUsageDisplay.tsx';
import EstimatedCostDisplay from '../shared/EstimatedCostDisplay.tsx';
import { sessionDetailPath } from '../../constants/routes.ts';
import { SESSION_STATUS } from '../../constants/sessionStatus.ts';
import type { DashboardSessionItem } from '../../types/session.ts';
import { actionStageChipStyles } from './sessionActionChipSx.ts';

interface SessionListItemProps {
  session: DashboardSessionItem;
  searchTerm: string;
  costEstimationEnabled?: boolean;
  onReviewClick?: (session: DashboardSessionItem) => void;
}

const iconOnlyChipSx = {
  height: 24,
  minWidth: 24,
  '& .MuiChip-label': { px: 0, display: 'none' },
  '& .MuiChip-icon': { mx: 0 },
} as const;

// The tooltip surface stays dark in both color schemes, so these match the dark palette
// rather than the light-mode tints, which disappear on grey.
const tokenTooltipNumberColor = {
  total: '#ffcc80',
  in: '#81d4fa',
  out: '#a5d6a7',
} as const;

function TokenBreakdownTooltip({ session }: { session: DashboardSessionItem }) {
  const lines: { label: string; value: number; color?: string }[] = [
    { label: 'total', value: session.total_tokens, color: tokenTooltipNumberColor.total },
    { label: 'in', value: session.input_tokens, color: tokenTooltipNumberColor.in },
    { label: 'out', value: session.output_tokens, color: tokenTooltipNumberColor.out },
  ];
  if (session.cache_read_tokens > 0) {
    lines.push({ label: 'cache read', value: session.cache_read_tokens });
  }
  if (session.cache_creation_tokens > 0) {
    lines.push({ label: 'cache create', value: session.cache_creation_tokens });
  }
  if (session.thinking_tokens > 0) {
    lines.push({ label: 'thinking', value: session.thinking_tokens });
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, minWidth: 132 }}>
      {lines.map((line) => (
        <Box
          key={line.label}
          sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 2 }}
        >
          <Box component="span">{line.label}</Box>
          <Box
            component="span"
            sx={{
              fontWeight: 700,
              fontVariantNumeric: 'tabular-nums',
              color: line.color ?? 'inherit',
            }}
          >
            {formatTokens(line.value)}
          </Box>
        </Box>
      ))}
    </Box>
  );
}

export function SessionListItem({
  session,
  searchTerm,
  costEstimationEnabled = false,
  onReviewClick,
}: SessionListItemProps) {
  const navigate = useNavigate();

  const handleRowClick = () => {
    navigate(sessionDetailPath(session.id));
  };

  return (
    <TableRow
      hover
      onClick={handleRowClick}
      sx={{
        cursor: 'pointer',
        '&:hover, &:focus-within': {
          backgroundColor: 'action.hover',
          '& .review-hover-icon': { opacity: 1 },
        },
      }}
    >
      {/* Status + Summary hover */}
      <TableCell sx={{ width: '1%', whiteSpace: 'nowrap', pr: 3 }}>
        <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 1 }}>
          <StatusBadge
            status={session.status}
            tooltip={
              session.status === SESSION_STATUS.CANCELLED
                ? formatCancelAttribution(session.cancelled_by, session.cancel_reason) || undefined
                : undefined
            }
          />
          <SummaryTooltip summary={session.executive_summary ?? ''} />
        </Box>
      </TableCell>

      {/* Session indicators: parallel, sub-agents, fallback, chat */}
      <TableCell sx={{ width: 130, textAlign: 'right', pl: 1.5, pr: 0.5 }}>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 0.5 }}>
          {session.has_parallel_stages && (
            <Tooltip title="Parallel Agents - Multiple agents run in parallel">
              <Chip
                icon={<CallSplit sx={{ fontSize: '0.875rem' }} />}
                size="small"
                color="secondary"
                variant="outlined"
                sx={iconOnlyChipSx}
              />
            </Tooltip>
          )}
          {session.has_sub_agents && (
            <Tooltip title="Orchestrator - Sub-agents dispatched">
              <Chip
                icon={<Hub sx={{ fontSize: '0.875rem' }} />}
                size="small"
                color="secondary"
                variant="outlined"
                sx={iconOnlyChipSx}
              />
            </Tooltip>
          )}
          {session.has_action_stages && (
            <Tooltip title={session.actions_executed ? 'Automated remediation actions executed' : 'Action agent ran — no actions taken'}>
              <Chip
                icon={<BuildOutlined sx={{ fontSize: '0.875rem' }} />}
                size="small"
                variant="outlined"
                sx={(theme) => ({
                  ...iconOnlyChipSx,
                  ...actionStageChipStyles(theme, !!session.actions_executed),
                })}
              />
            </Tooltip>
          )}
          {session.provider_fallback_count > 0 && (
            <Tooltip
              title={`Provider fallback${session.provider_fallback_count > 1 ? ` (${session.provider_fallback_count}×)` : ''}`}
            >
              <Chip
                icon={<SwapHoriz sx={{ fontSize: '0.875rem' }} />}
                size="small"
                color="warning"
                variant="outlined"
                sx={iconOnlyChipSx}
              />
            </Tooltip>
          )}
          {session.chat_message_count > 0 && (
            <Tooltip
              title={`Follow-up chat active (${session.chat_message_count} message${session.chat_message_count !== 1 ? 's' : ''})`}
            >
              <Chip
                icon={<ChatIcon sx={{ fontSize: '0.875rem' }} />}
                size="small"
                color="primary"
                variant="outlined"
                sx={iconOnlyChipSx}
              />
            </Tooltip>
          )}
          {searchTerm && session.matched_in_content && (
            <Tooltip title="Search matched in session content">
              <Chip
                icon={<FindInPage sx={{ fontSize: '0.875rem' }} />}
                size="small"
                color="info"
                variant="outlined"
                sx={iconOnlyChipSx}
              />
            </Tooltip>
          )}
        </Box>
      </TableCell>

      {/* Alert Type */}
      <TableCell>
        <Typography variant="body2" sx={{ fontWeight: 500 }}>
          {highlightSearchTermNodes(session.alert_type ?? '', searchTerm)}
        </Typography>
      </TableCell>

      <TableCell sx={{ width: '1%', whiteSpace: 'nowrap' }}>
        <SessionLabelChips labels={session.labels} />
      </TableCell>

      {/* Submitted by */}
      <TableCell>
        <Typography variant="body2" sx={{
          color: 'text.secondary'
        }}>
          {session.author ?? '—'}
        </Typography>
      </TableCell>

      {/* Time */}
      <TableCell>
        <Tooltip title={formatTimestamp(session.created_at, 'absolute')}>
          <Typography variant="body2" sx={{
            color: 'text.secondary'
          }}>
            {formatTimestamp(session.created_at, 'short')}
          </Typography>
        </Tooltip>
      </TableCell>

      {/* Duration */}
      <TableCell>
        <Typography variant="body2" sx={{
          color: 'text.secondary'
        }}>
          {formatDurationMs(session.duration_ms)}
        </Typography>
      </TableCell>

      {/* Tokens */}
      <TableCell>
        {(session.total_tokens > 0 || session.input_tokens > 0 || session.output_tokens > 0) ? (
          <Tooltip
            title={<TokenBreakdownTooltip session={session} />}
            slotProps={{
              tooltip: {
                sx: {
                  // Same dark-grey tooltip family, but solid and one step darker
                  // than the default translucent grey so the numbers stay readable.
                  bgcolor: 'grey.800',
                },
              },
            }}
          >
            <Box component="span" sx={{ display: 'inline-flex' }}>
              <TokenUsageDisplay
                tokenData={{
                  input_tokens: session.input_tokens,
                  output_tokens: session.output_tokens,
                  total_tokens: session.total_tokens,
                }}
                variant="inline"
                size="small"
                showBreakdown={false}
              />
            </Box>
          </Tooltip>
        ) : (
          <Typography variant="body2" sx={{
            color: 'text.secondary'
          }}>
            —
          </Typography>
        )}
      </TableCell>

      {/* Est. Cost — only rendered when cost estimation is enabled */}
      {costEstimationEnabled && (
        <TableCell>
          {session.estimated_cost_usd != null && session.cost_completeness != null && session.cost_completeness !== 'none' ? (
            <EstimatedCostDisplay
              enabled
              estimatedCostUsd={session.estimated_cost_usd}
              costCompleteness={session.cost_completeness}
              size="small"
            />
          ) : (
            <Typography variant="body2" sx={{
              color: 'text.secondary'
            }}>
              —
            </Typography>
          )}
        </TableCell>
      )}

      {/* Eval Score */}
      <ScoreCell
        sessionId={session.id}
        score={session.latest_score}
        scoringStatus={session.scoring_status}
        sx={qualityEvalScoreBodySx}
      />

      {/* Review */}
      <ReviewCell session={session} onReviewClick={onReviewClick} />

      {/* Actions */}
      <TableCell sx={{ width: 60, textAlign: 'center' }}>
        <OpenNewTabButton sessionId={session.id} />
      </TableCell>
    </TableRow>
  );
}
