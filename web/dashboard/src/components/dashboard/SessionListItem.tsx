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
  ThumbUp,
  ThumbsUpDown,
  ThumbDown,
} from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { SummaryTooltip } from './SummaryTooltip.tsx';
import { ScoreCell } from './ScoreCell.tsx';
import { qualityEvalScoreBodySx, qualityReviewBodySx } from './qualityGroupSx.ts';
import { OpenNewTabButton } from './OpenNewTabButton.tsx';
import { highlightSearchTermNodes } from '../../utils/search.ts';
import { formatTimestamp, formatDurationMs } from '../../utils/format.ts';
import TokenUsageDisplay from '../shared/TokenUsageDisplay.tsx';
import { sessionDetailPath } from '../../constants/routes.ts';
import { QUALITY_RATING } from '../../types/api.ts';
import type { DashboardSessionItem } from '../../types/session.ts';

interface SessionListItemProps {
  session: DashboardSessionItem;
  searchTerm: string;
  onReviewClick?: (session: DashboardSessionItem) => void;
}

const iconOnlyChipSx = {
  height: 24,
  minWidth: 24,
  '& .MuiChip-label': { px: 0, display: 'none' },
  '& .MuiChip-icon': { mx: 0 },
} as const;

const TERMINAL_STATUSES = new Set(['completed', 'failed', 'cancelled']);

const RATING_CHIP_MAP: Record<string, {
  icon: React.ReactElement;
  chipColor: 'success' | 'warning' | 'error';
  label: string;
}> = {
  [QUALITY_RATING.ACCURATE]: { icon: <ThumbUp sx={{ fontSize: '0.875rem' }} />, chipColor: 'success', label: 'Accurate' },
  [QUALITY_RATING.PARTIALLY_ACCURATE]: { icon: <ThumbsUpDown sx={{ fontSize: '0.875rem' }} />, chipColor: 'warning', label: 'Partially Accurate' },
  [QUALITY_RATING.INACCURATE]: { icon: <ThumbDown sx={{ fontSize: '0.875rem' }} />, chipColor: 'error', label: 'Inaccurate' },
};

export function SessionListItem({ session, searchTerm, onReviewClick }: SessionListItemProps) {
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
        '&:hover': {
          backgroundColor: 'action.hover',
          '& .review-hover-icon': { opacity: 1 },
        },
      }}
    >
      {/* Status + Summary hover */}
      <TableCell>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <StatusBadge status={session.status} />
          <SummaryTooltip summary={session.executive_summary ?? ''} />
        </Box>
      </TableCell>

      {/* Session indicators: parallel, sub-agents, fallback, chat */}
      <TableCell sx={{ width: 130, textAlign: 'right', px: 0.5 }}>
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
            <Tooltip title="Action Evaluation - Automated remediation evaluated">
              <Chip
                icon={<BuildOutlined sx={{ fontSize: '0.875rem' }} />}
                size="small"
                color="success"
                variant="outlined"
                sx={iconOnlyChipSx}
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

      {/* Submitted by */}
      <TableCell>
        <Typography variant="body2" color="text.secondary">
          {session.author ?? '—'}
        </Typography>
      </TableCell>

      {/* Time */}
      <TableCell>
        <Tooltip title={formatTimestamp(session.created_at, 'absolute')}>
          <Typography variant="body2" color="text.secondary">
            {formatTimestamp(session.created_at, 'short')}
          </Typography>
        </Tooltip>
      </TableCell>

      {/* Duration */}
      <TableCell>
        <Typography variant="body2" color="text.secondary">
          {formatDurationMs(session.duration_ms)}
        </Typography>
      </TableCell>

      {/* Tokens */}
      <TableCell>
        {(session.total_tokens > 0 || session.input_tokens > 0 || session.output_tokens > 0) ? (
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
        ) : (
          <Typography variant="body2" color="text.secondary">
            —
          </Typography>
        )}
      </TableCell>

      {/* Eval Score */}
      <ScoreCell
        sessionId={session.id}
        score={session.latest_score}
        scoringStatus={session.scoring_status}
        sx={qualityEvalScoreBodySx}
      />

      {/* Review */}
      <TableCell sx={qualityReviewBodySx}>
        {(() => {
          const isTerminal = TERMINAL_STATUSES.has(session.status);
          if (!isTerminal) return null;

          const rating = session.quality_rating ? RATING_CHIP_MAP[session.quality_rating] : null;

          if (rating) {
            return (
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
            );
          }

          return (
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
          );
        })()}
      </TableCell>

      {/* Actions */}
      <TableCell sx={{ width: 60, textAlign: 'center' }}>
        <OpenNewTabButton sessionId={session.id} />
      </TableCell>
    </TableRow>
  );
}
