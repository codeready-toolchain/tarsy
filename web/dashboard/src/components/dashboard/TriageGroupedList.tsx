import { useState, useCallback, useEffect } from 'react';
import {
  Box,
  Paper,
  Typography,
  Chip,
  Collapse,
  IconButton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Checkbox,
  Button,
} from '@mui/material';
import {
  ExpandMore,
  ExpandLess,
  Search as SearchIcon,
  RateReview,
  AssignmentTurnedIn,
  CheckCircleOutline,
  Close,
} from '@mui/icons-material';
import { PaginationControls } from './PaginationControls.tsx';
import { TriageSessionRow, type TriageGroup as TriageGroupName } from './TriageSessionRow.tsx';
import { QualityGroupHeaders } from './QualityGroupHeaders.tsx';
import type { TriageGroup, TriageGroupKey } from '../../types/api.ts';

interface TriageGroupedListProps {
  groups: Record<TriageGroupKey, TriageGroup | null>;
  onClaim: (sessionId: string) => void;
  onUnclaim: (sessionId: string) => void;
  onReopen: (sessionId: string) => void;
  onReviewClick: (session: import('../../types/session.ts').DashboardSessionItem) => void;
  onPageChange: (group: TriageGroupKey, page: number) => void;
  onPageSizeChange: (group: TriageGroupKey, pageSize: number) => void;
  onBulkClaim?: (sessionIds: string[]) => void;
  onBulkComplete?: (sessionIds: string[]) => void;
  onBulkUnclaim?: (sessionIds: string[]) => void;
  onBulkReopen?: (sessionIds: string[]) => void;
  actionLoading?: boolean;
}

interface GroupConfig {
  key: TriageGroupName;
  label: string;
  dataKey: TriageGroupKey;
  icon: React.ReactElement;
  defaultOpen: boolean;
  color: string;
  accentBorder?: boolean;
}

const groups_config: GroupConfig[] = [
  {
    key: 'investigating',
    label: 'Investigating',
    dataKey: 'investigating',
    icon: <SearchIcon sx={{ fontSize: 18 }} />,
    defaultOpen: true,
    color: 'primary.main',
  },
  {
    key: 'needs_review',
    label: 'Needs Review',
    dataKey: 'needs_review',
    icon: <RateReview sx={{ fontSize: 18 }} />,
    defaultOpen: true,
    color: 'warning.main',
    accentBorder: true,
  },
  {
    key: 'in_progress',
    label: 'In Review',
    dataKey: 'in_progress',
    icon: <AssignmentTurnedIn sx={{ fontSize: 18 }} />,
    defaultOpen: true,
    color: 'info.main',
  },
  {
    key: 'reviewed',
    label: 'Reviewed',
    dataKey: 'reviewed',
    icon: <CheckCircleOutline sx={{ fontSize: 18 }} />,
    defaultOpen: false,
    color: 'success.main',
  },
];

const SELECTABLE_GROUPS = new Set<TriageGroupName>(['needs_review', 'in_progress', 'reviewed']);
const MAX_BULK_SELECTION = 50;

type SelectionState = Record<TriageGroupKey, Set<string>>;

const emptySelection = (): SelectionState => ({
  investigating: new Set(),
  needs_review: new Set(),
  in_progress: new Set(),
  reviewed: new Set(),
});

export function TriageGroupedList({
  groups,
  onClaim,
  onUnclaim,
  onReopen,
  onReviewClick,
  onPageChange,
  onPageSizeChange,
  onBulkClaim,
  onBulkComplete,
  onBulkUnclaim,
  onBulkReopen,
  actionLoading,
}: TriageGroupedListProps) {
  const STORAGE_KEY = 'triage-open-sections';

  const [openSections, setOpenSections] = useState<Record<string, boolean>>(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      if (stored) return JSON.parse(stored);
    } catch { /* ignore */ }
    const initial: Record<string, boolean> = {};
    for (const g of groups_config) {
      initial[g.key] = g.defaultOpen;
    }
    return initial;
  });

  const [selection, setSelection] = useState<SelectionState>(emptySelection);

  // Clear selection when group data changes (e.g. after a bulk action refetch).
  useEffect(() => {
    setSelection(emptySelection());
  }, [groups]);

  const toggleSection = (key: string) => {
    setOpenSections((prev) => {
      const next = { ...prev, [key]: !prev[key] };
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)); } catch { /* ignore */ }
      return next;
    });
  };

  const toggleSelect = useCallback((groupKey: TriageGroupKey, sessionId: string) => {
    setSelection((prev) => {
      const next = new Set(prev[groupKey]);
      if (next.has(sessionId)) {
        next.delete(sessionId);
      } else if (next.size < MAX_BULK_SELECTION) {
        next.add(sessionId);
      }
      return { ...prev, [groupKey]: next };
    });
  }, []);

  const toggleSelectAll = useCallback((groupKey: TriageGroupKey, sessionIds: string[]) => {
    setSelection((prev) => {
      const current = prev[groupKey];
      const allSelected = sessionIds.length > 0 && sessionIds.every((id) => current.has(id));
      return {
        ...prev,
        [groupKey]: allSelected
          ? new Set<string>()
          : new Set(sessionIds.slice(0, MAX_BULK_SELECTION)),
      };
    });
  }, []);

  const clearGroupSelection = useCallback((groupKey: TriageGroupKey) => {
    setSelection((prev) => ({ ...prev, [groupKey]: new Set<string>() }));
  }, []);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      {groups_config.map((group) => {
        const groupData = groups[group.dataKey];
        if (!groupData) return null;
        const isOpen = openSections[group.key];
        const isEmpty = groupData.count === 0;
        const selectable = SELECTABLE_GROUPS.has(group.key);
        const selectedIds = selection[group.dataKey];
        const selectedCount = selectedIds.size;
        const atSelectionLimit = selectedCount >= MAX_BULK_SELECTION;
        const visibleIds = groupData.sessions.map((s) => s.id);
        const allVisibleSelected = visibleIds.length > 0 && visibleIds.every((id) => selectedIds.has(id));
        const someVisibleSelected = visibleIds.some((id) => selectedIds.has(id));

        if (group.key === 'investigating' && isEmpty) {
          return (
            <Paper key={group.key} variant="outlined">
              <Box
                onClick={() => toggleSection(group.key)}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 1,
                  px: 2,
                  py: 0.75,
                  cursor: 'pointer',
                  userSelect: 'none',
                  '&:hover': { backgroundColor: 'action.hover' },
                }}
              >
                <Box sx={{ color: group.color, display: 'flex', alignItems: 'center' }}>
                  {group.icon}
                </Box>
                <Typography variant="subtitle2" fontWeight={600} color="text.secondary">
                  {group.label}
                </Typography>
                <Chip
                  label={0}
                  size="small"
                  sx={{
                    height: 20,
                    minWidth: 24,
                    fontSize: '0.7rem',
                    fontWeight: 600,
                    backgroundColor: 'action.disabledBackground',
                    color: 'text.disabled',
                  }}
                />
                <Typography variant="caption" color="text.disabled" sx={{ ml: 0.5 }}>
                  No active investigations
                </Typography>
              </Box>
            </Paper>
          );
        }

        return (
          <Paper
            key={group.key}
            variant="outlined"
            sx={{
              overflow: 'hidden',
              ...(group.accentBorder && !isEmpty && {
                borderLeft: '3px solid',
                borderLeftColor: group.color,
              }),
            }}
          >
            {/* Group header */}
            <Box
              onClick={() => toggleSection(group.key)}
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 1,
                px: 2,
                py: 1,
                cursor: 'pointer',
                userSelect: 'none',
                backgroundColor: 'background.default',
                borderBottom: isOpen && !isEmpty ? '1px solid' : 'none',
                borderColor: 'divider',
                '&:hover': { backgroundColor: 'action.hover' },
              }}
            >
              <Box sx={{ color: group.color, display: 'flex', alignItems: 'center' }}>
                {group.icon}
              </Box>
              <Typography variant="subtitle2" fontWeight={600} sx={{ flexGrow: 1 }}>
                {group.label}
              </Typography>
              <Chip
                label={groupData.count}
                size="small"
                sx={{
                  height: 22,
                  minWidth: 28,
                  fontSize: '0.75rem',
                  fontWeight: 600,
                  backgroundColor: isEmpty ? 'action.disabledBackground' : group.color,
                  color: isEmpty ? 'text.disabled' : 'common.white',
                }}
              />
              <IconButton size="small" sx={{ ml: 0.5 }}>
                {isOpen ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
              </IconButton>
            </Box>

            {/* Group content */}
            <Collapse in={isOpen}>
              {isEmpty ? (
                <Box sx={{ px: 2, py: 2.5, textAlign: 'center' }}>
                  <Typography variant="body2" color="text.secondary">
                    No sessions
                  </Typography>
                </Box>
              ) : (
                <>
                  {/* Bulk action toolbar */}
                  {selectable && selectedCount > 0 && (
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 1,
                        px: 2,
                        py: 0.75,
                        backgroundColor: 'action.selected',
                        borderBottom: '1px solid',
                        borderColor: 'divider',
                      }}
                    >
                      <Typography variant="body2" fontWeight={600} sx={{ mr: 1 }}>
                        {selectedCount} selected{atSelectionLimit ? ` (max ${MAX_BULK_SELECTION})` : ''}
                      </Typography>

                      {group.key === 'needs_review' && (
                        <>
                          {onBulkClaim && (
                            <Button
                              size="small"
                              variant="contained"
                              disabled={actionLoading}
                              onClick={() => onBulkClaim([...selectedIds])}
                              sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
                            >
                              Claim All
                            </Button>
                          )}
                          {onBulkComplete && (
                            <Button
                              size="small"
                              variant="contained"
                              color="success"
                              disabled={actionLoading}
                              onClick={() => onBulkComplete([...selectedIds])}
                              sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
                            >
                              Complete All
                            </Button>
                          )}
                        </>
                      )}

                      {group.key === 'in_progress' && (
                        <>
                          {onBulkComplete && (
                            <Button
                              size="small"
                              variant="contained"
                              color="success"
                              disabled={actionLoading}
                              onClick={() => onBulkComplete([...selectedIds])}
                              sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
                            >
                              Complete All
                            </Button>
                          )}
                          {onBulkUnclaim && (
                            <Button
                              size="small"
                              variant="outlined"
                              disabled={actionLoading}
                              onClick={() => onBulkUnclaim([...selectedIds])}
                              sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
                            >
                              Unclaim All
                            </Button>
                          )}
                        </>
                      )}

                      {group.key === 'reviewed' && onBulkReopen && (
                        <Button
                          size="small"
                          variant="outlined"
                          disabled={actionLoading}
                          onClick={() => onBulkReopen([...selectedIds])}
                          sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
                        >
                          Reopen All
                        </Button>
                      )}

                      <Box sx={{ flexGrow: 1 }} />
                      <IconButton size="small" onClick={() => clearGroupSelection(group.dataKey)}>
                        <Close sx={{ fontSize: 16 }} />
                      </IconButton>
                    </Box>
                  )}

                  <TableContainer>
                    <Table size="small">
                      <TableHead>
                        <TableRow>
                          {selectable && (
                            <TableCell padding="checkbox">
                              <Checkbox
                                size="small"
                                checked={allVisibleSelected}
                                indeterminate={someVisibleSelected && !allVisibleSelected}
                                disabled={atSelectionLimit && !allVisibleSelected}
                                onChange={() => toggleSelectAll(group.dataKey, visibleIds)}
                              />
                            </TableCell>
                          )}
                          <TableCell sx={{ fontWeight: 600 }}>Status</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Type</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Submitted by</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Assignee</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Time</TableCell>
                          <QualityGroupHeaders />
                          <TableCell sx={{ fontWeight: 600, width: 100, textAlign: 'right' }} />
                        </TableRow>
                      </TableHead>
                      <TableBody>
                        {groupData.sessions.map((session) => (
                          <TriageSessionRow
                            key={session.id}
                            session={session}
                            group={group.key}
                            selected={selectedIds.has(session.id)}
                            selectionDisabled={atSelectionLimit}
                            onToggleSelect={selectable ? (id) => toggleSelect(group.dataKey, id) : undefined}
                            onClaim={onClaim}
                            onUnclaim={onUnclaim}
                            onReopen={onReopen}
                            onReviewClick={onReviewClick}
                            actionLoading={actionLoading}
                          />
                        ))}
                      </TableBody>
                    </Table>
                  </TableContainer>
                  {groupData.count > 10 && (
                    <Box sx={{ borderTop: '1px solid', borderColor: 'divider' }}>
                      <PaginationControls
                        pagination={{
                          page: groupData.page,
                          pageSize: groupData.page_size,
                          totalPages: groupData.total_pages,
                          totalItems: groupData.count,
                        }}
                        onPageChange={(page) => onPageChange(group.dataKey, page)}
                        onPageSizeChange={(pageSize) => onPageSizeChange(group.dataKey, pageSize)}
                      />
                    </Box>
                  )}
                </>
              )}
            </Collapse>
          </Paper>
        );
      })}
    </Box>
  );
}
