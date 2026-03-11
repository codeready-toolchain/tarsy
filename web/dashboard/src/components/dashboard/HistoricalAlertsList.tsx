/**
 * HistoricalAlertsList — sortable, paginated table of completed/failed/cancelled sessions.
 *
 * Ported from old dashboard's HistoricalAlertsList.tsx.
 * Adapted for new backend: sort fields match Go API (created_at, status, alert_type, author, duration).
 */

import {
  Paper,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
  CircularProgress,
  Alert,
  Box,
  Chip,
  IconButton,
  TableSortLabel,
  Tooltip,
} from '@mui/material';
import { Refresh, SearchOff, CallSplit, Hub, BuildOutlined, SmsOutlined as ChatIcon, SwapHoriz } from '@mui/icons-material';
import { SessionListItem } from './SessionListItem.tsx';
import { PaginationControls } from './PaginationControls.tsx';
import { hasActiveFilters } from '../../utils/search.ts';
import type { DashboardSessionItem } from '../../types/session.ts';
import type { SessionFilter, PaginationState, SortState } from '../../types/dashboard.ts';

/**
 * Column order: Status | Indicators | Type | Author | Time | Duration | Tokens | Actions
 * Indicators column packs: parallel, sub-agents, action, fallback, chat (fixed-slot grid).
 */
const TOTAL_COLUMNS = 9;

interface HistoricalAlertsListProps {
  sessions: DashboardSessionItem[];
  loading: boolean;
  error: string | null;
  filters: SessionFilter;
  filteredCount: number;
  sortState: SortState;
  pagination: PaginationState;
  onRefresh: () => void;
  onSortChange: (field: string) => void;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
}

export function HistoricalAlertsList({
  sessions,
  loading,
  error,
  filters,
  filteredCount,
  sortState,
  pagination,
  onRefresh,
  onSortChange,
  onPageChange,
  onPageSizeChange,
}: HistoricalAlertsListProps) {
  return (
    <Paper variant="outlined" sx={{ overflow: 'hidden' }}>
      {/* Header */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          px: 2,
          py: 1,
          backgroundColor: 'background.default',
          borderBottom: '1px solid',
          borderColor: 'divider',
        }}
      >
        <Typography variant="subtitle2" fontWeight={600} sx={{ flexGrow: 1 }}>
          Alert History
        </Typography>
        <Chip
          label={filteredCount.toLocaleString()}
          size="small"
          sx={{
            height: 22,
            minWidth: 28,
            fontSize: '0.75rem',
            fontWeight: 600,
          }}
        />
        <IconButton
          size="small"
          onClick={onRefresh}
          disabled={loading}
        >
          {loading ? <CircularProgress size={16} /> : <Refresh fontSize="small" />}
        </IconButton>
      </Box>

      {/* Error */}
      {error && (
        <Alert severity="error" sx={{ mx: 2, mb: 1 }}>
          {error}
        </Alert>
      )}

      {/* Loading */}
      {loading && sessions.length === 0 ? (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: 8 }}>
          <CircularProgress />
        </Box>
      ) : (
        <>
          <TableContainer>
            <Table size="small">
              <TableHead>
                <TableRow>
                  {/* Status — sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>
                    <TableSortLabel
                      active={sortState.field === 'status'}
                      direction={sortState.field === 'status' ? sortState.direction : 'asc'}
                      onClick={() => onSortChange('status')}
                    >
                      Status
                    </TableSortLabel>
                  </TableCell>

                  {/* Session indicators: parallel, sub-agents, action, fallback, chat */}
                  <TableCell sx={{ width: 130, px: 0.5, textAlign: 'right' }}>
                    <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 0.5 }}>
                      <Tooltip title="Parallel Agents" arrow>
                        <CallSplit
                          sx={{
                            fontSize: '1.1rem',
                            color: 'secondary.main',
                            verticalAlign: 'middle',
                            cursor: 'help',
                          }}
                        />
                      </Tooltip>
                      <Tooltip title="Orchestrator / Sub-agents" arrow>
                        <Hub
                          sx={{
                            fontSize: '1.1rem',
                            color: 'secondary.main',
                            verticalAlign: 'middle',
                            cursor: 'help',
                          }}
                        />
                      </Tooltip>
                      <Tooltip title="Automated Action" arrow>
                        <BuildOutlined
                          sx={{
                            fontSize: '1.1rem',
                            color: 'success.main',
                            verticalAlign: 'middle',
                            cursor: 'help',
                          }}
                        />
                      </Tooltip>
                      <Tooltip title="Provider Fallback" arrow>
                        <SwapHoriz
                          sx={{
                            fontSize: '1.1rem',
                            color: 'warning.main',
                            verticalAlign: 'middle',
                            cursor: 'help',
                          }}
                        />
                      </Tooltip>
                      <Tooltip title="Follow-up Chats" arrow>
                        <ChatIcon
                          sx={{
                            fontSize: '1.1rem',
                            color: 'primary.main',
                            verticalAlign: 'middle',
                            cursor: 'help',
                          }}
                        />
                      </Tooltip>
                    </Box>
                  </TableCell>

                  {/* Type — sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>
                    <TableSortLabel
                      active={sortState.field === 'alert_type'}
                      direction={sortState.field === 'alert_type' ? sortState.direction : 'asc'}
                      onClick={() => onSortChange('alert_type')}
                    >
                      Type
                    </TableSortLabel>
                  </TableCell>

                  {/* Submitted by — sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>
                    <TableSortLabel
                      active={sortState.field === 'author'}
                      direction={sortState.field === 'author' ? sortState.direction : 'asc'}
                      onClick={() => onSortChange('author')}
                    >
                      Submitted by
                    </TableSortLabel>
                  </TableCell>

                  {/* Time — sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>
                    <TableSortLabel
                      active={sortState.field === 'created_at'}
                      direction={sortState.field === 'created_at' ? sortState.direction : 'asc'}
                      onClick={() => onSortChange('created_at')}
                    >
                      Time
                    </TableSortLabel>
                  </TableCell>

                  {/* Duration — sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>
                    <TableSortLabel
                      active={sortState.field === 'duration'}
                      direction={sortState.field === 'duration' ? sortState.direction : 'asc'}
                      onClick={() => onSortChange('duration')}
                    >
                      Duration
                    </TableSortLabel>
                  </TableCell>

                  {/* Eval Score — sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>
                    <TableSortLabel
                      active={sortState.field === 'score'}
                      direction={sortState.field === 'score' ? sortState.direction : 'desc'}
                      onClick={() => onSortChange('score')}
                    >
                      Eval Score
                    </TableSortLabel>
                  </TableCell>

                  {/* Tokens — not sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>Tokens</TableCell>

                  {/* Actions */}
                  <TableCell sx={{ fontWeight: 600, width: 60, textAlign: 'center' }} />
                </TableRow>
              </TableHead>

              <TableBody>
                {sessions.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={TOTAL_COLUMNS} align="center">
                      <Box sx={{ py: 6, textAlign: 'center' }}>
                        {hasActiveFilters(filters) ? (
                          <>
                            <SearchOff sx={{ fontSize: 48, color: 'text.secondary', mb: 2 }} />
                            <Typography variant="h6" color="text.secondary" gutterBottom>
                              No alerts found
                            </Typography>
                            <Typography variant="body2" color="text.disabled">
                              Try adjusting your search terms or filters
                            </Typography>
                          </>
                        ) : (
                          <>
                            <Typography variant="h6" color="text.secondary" gutterBottom>
                              No Historical Alerts
                            </Typography>
                            <Typography variant="body2" color="text.secondary">
                              No completed, failed, or cancelled alerts found.
                            </Typography>
                          </>
                        )}
                      </Box>
                    </TableCell>
                  </TableRow>
                ) : (
                  sessions.map((session) => (
                    <SessionListItem
                      key={session.id}
                      session={session}
                      searchTerm={filters.search}
                    />
                  ))
                )}
              </TableBody>
            </Table>
          </TableContainer>

          {/* Pagination */}
          <PaginationControls
            pagination={pagination}
            onPageChange={onPageChange}
            onPageSizeChange={onPageSizeChange}
            disabled={loading}
          />
        </>
      )}
    </Paper>
  );
}
