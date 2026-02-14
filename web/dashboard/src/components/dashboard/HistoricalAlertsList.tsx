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
  Button,
  TableSortLabel,
  Tooltip,
} from '@mui/material';
import { Refresh, SearchOff, CallSplit, Chat as ChatIcon } from '@mui/icons-material';
import { SessionListItem } from './SessionListItem.tsx';
import { PaginationControls } from './PaginationControls.tsx';
import { hasActiveFilters } from '../../utils/search.ts';
import type { DashboardSessionItem } from '../../types/session.ts';
import type { SessionFilter, PaginationState, SortState } from '../../types/dashboard.ts';

/**
 * Column order matches old dashboard: Status | Parallel | Type | Chain | Author | Time | Duration | Tokens | Chat | Actions
 * Chain is non-sortable; the rest of the named columns are sortable.
 */
const TOTAL_COLUMNS = 10;

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
    <Paper sx={{ p: 3 }}>
      {/* Header */}
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant="h5" sx={{ fontWeight: 600 }}>
          Alert History
          <Typography component="span" variant="body2" color="text.secondary" sx={{ ml: 1 }}>
            ({filteredCount.toLocaleString()} result{filteredCount !== 1 ? 's' : ''})
          </Typography>
        </Typography>

        <Button
          variant="outlined"
          size="small"
          startIcon={loading ? <CircularProgress size={16} /> : <Refresh />}
          onClick={onRefresh}
          disabled={loading}
        >
          {loading ? 'Loading...' : 'Refresh'}
        </Button>
      </Box>

      {/* Error */}
      {error && (
        <Alert severity="error" sx={{ mb: 2 }}>
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
            <Table>
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

                  {/* Parallel indicator */}
                  <TableCell sx={{ width: 40, px: 0.5, textAlign: 'center' }}>
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

                  {/* Agent Chain — not sortable (matches old dashboard column order) */}
                  <TableCell sx={{ fontWeight: 600 }}>Agent Chain</TableCell>

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

                  {/* Tokens — not sortable */}
                  <TableCell sx={{ fontWeight: 600 }}>Tokens</TableCell>

                  {/* Chat indicator */}
                  <TableCell sx={{ width: 40, px: 0.5, textAlign: 'center' }}>
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
                  </TableCell>

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
