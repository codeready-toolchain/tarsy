/**
 * SessionListItem — single row in the historical sessions table.
 *
 * Ported from old dashboard's AlertListItem.tsx.
 * Adapted for new backend types: `id` instead of `session_id`,
 * RFC3339 timestamps, `total_tokens` instead of `session_total_tokens`.
 */

import { useState } from 'react';
import {
  TableRow,
  TableCell,
  Typography,
  IconButton,
  Tooltip,
  Chip,
  Box,
  Popover,
  Card,
  Divider,
} from '@mui/material';
import {
  OpenInNew,
  Chat as ChatIcon,
  CallSplit,
  Hub,
  Summarize,
} from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import ReactMarkdown from 'react-markdown';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { highlightSearchTermNodes } from '../../utils/search.ts';
import { formatTimestamp, formatDurationMs } from '../../utils/format.ts';
import TokenUsageDisplay from '../shared/TokenUsageDisplay.tsx';
import { sessionDetailPath } from '../../constants/routes.ts';
import { executiveSummaryMarkdownStyles } from '../../utils/markdownComponents.tsx';
import type { DashboardSessionItem } from '../../types/session.ts';

interface SessionListItemProps {
  session: DashboardSessionItem;
  searchTerm: string;
}

const iconOnlyChipSx = {
  height: 24,
  minWidth: 24,
  '& .MuiChip-label': { px: 0, display: 'none' },
  '& .MuiChip-icon': { mx: 0 },
} as const;

export function SessionListItem({ session, searchTerm }: SessionListItemProps) {
  const navigate = useNavigate();
  const [summaryAnchorEl, setSummaryAnchorEl] = useState<HTMLElement | null>(null);

  const hasSummary =
    session.executive_summary && session.executive_summary.trim().length > 0;

  const handleRowClick = () => {
    navigate(sessionDetailPath(session.id));
  };

  const handleNewTabClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    window.open(
      `${window.location.origin}${sessionDetailPath(session.id)}`,
      '_blank',
      'noopener,noreferrer',
    );
  };

  return (
    <TableRow
      hover
      onClick={handleRowClick}
      sx={{ cursor: 'pointer', '&:hover': { backgroundColor: 'action.hover' } }}
    >
      {/* Status + Summary hover */}
      <TableCell>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <StatusBadge status={session.status} />
          {hasSummary && (
            <>
              <Chip
                label="Summary"
                size="small"
                variant="outlined"
                color="primary"
                onMouseEnter={(e) => setSummaryAnchorEl(e.currentTarget)}
                onMouseLeave={() => setSummaryAnchorEl(null)}
                onClick={(e) => e.stopPropagation()}
                sx={{
                  cursor: 'pointer',
                  height: 24,
                  fontSize: '0.75rem',
                  fontWeight: 500,
                  transition: 'all 0.2s ease-in-out',
                  '&:hover': (theme) => ({
                    backgroundColor: `${theme.palette.grey[700]} !important`,
                    color: `${theme.palette.common.white} !important`,
                    borderColor: `${theme.palette.grey[700]} !important`,
                  }),
                }}
              />
              <Popover
                sx={{ pointerEvents: 'none' }}
                open={Boolean(summaryAnchorEl)}
                anchorEl={summaryAnchorEl}
                anchorOrigin={{ vertical: 'top', horizontal: 'left' }}
                transformOrigin={{ vertical: 'bottom', horizontal: 'left' }}
                onClose={() => setSummaryAnchorEl(null)}
                disableRestoreFocus
              >
                <Card sx={{ maxWidth: 500, p: 2.5, boxShadow: 3 }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
                    <Summarize color="primary" />
                    <Typography
                      variant="subtitle1"
                      sx={{ fontWeight: 600, color: 'primary.main' }}
                    >
                      Executive Summary
                    </Typography>
                  </Box>
                  <Divider sx={{ mb: 1.5 }} />
                  <Box sx={executiveSummaryMarkdownStyles}>
                    <ReactMarkdown skipHtml>{session.executive_summary}</ReactMarkdown>
                  </Box>
                </Card>
              </Popover>
            </>
          )}
        </Box>
      </TableCell>

      {/* Parallel / Sub-agent indicators */}
      <TableCell sx={{ width: 60, textAlign: 'center', px: 0.5 }}>
        <Box sx={{ display: 'flex', justifyContent: 'center', gap: 0.5 }}>
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
        </Box>
      </TableCell>

      {/* Alert Type */}
      <TableCell>
        <Typography variant="body2" sx={{ fontWeight: 500 }}>
          {highlightSearchTermNodes(session.alert_type ?? '', searchTerm)}
        </Typography>
      </TableCell>

      {/* Agent Chain */}
      <TableCell>
        <Typography variant="body2">{session.chain_id}</Typography>
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

      {/* Chat indicator */}
      <TableCell sx={{ width: 40, textAlign: 'center', px: 0.5 }}>
        {session.chat_message_count > 0 && (
          <Tooltip
            title={`Follow-up chat active (${session.chat_message_count} message${session.chat_message_count !== 1 ? 's' : ''})`}
          >
            <Chip
              icon={<ChatIcon sx={{ fontSize: '0.875rem' }} />}
              size="small"
              color="primary"
              variant="outlined"
              sx={{
                height: 24,
                minWidth: 24,
                '& .MuiChip-label': { px: 0, display: 'none' },
                '& .MuiChip-icon': { mx: 0 },
              }}
            />
          </Tooltip>
        )}
      </TableCell>

      {/* Actions */}
      <TableCell sx={{ width: 60, textAlign: 'center' }}>
        <Tooltip title="Open in new tab">
          <IconButton
            size="small"
            onClick={handleNewTabClick}
            sx={{ opacity: 0.7, '&:hover': { opacity: 1, backgroundColor: 'action.hover' } }}
          >
            <OpenInNew fontSize="small" />
          </IconButton>
        </Tooltip>
      </TableCell>
    </TableRow>
  );
}
