import { useState } from 'react';
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
} from '@mui/material';
import {
  ExpandMore,
  ExpandLess,
  Search as SearchIcon,
  RateReview,
  AssignmentTurnedIn,
  CheckCircleOutline,
} from '@mui/icons-material';
import { PaginationControls } from './PaginationControls.tsx';
import { TriageSessionRow, type TriageGroup as TriageGroupName } from './TriageSessionRow.tsx';
import type { TriageGroup, TriageGroupKey } from '../../types/api.ts';

interface TriageGroupedListProps {
  groups: Record<TriageGroupKey, TriageGroup | null>;
  onClaim: (sessionId: string) => void;
  onUnclaim: (sessionId: string) => void;
  onResolve: (sessionId: string) => void;
  onReopen: (sessionId: string) => void;
  onEditNote: (sessionId: string, currentNote: string) => void;
  onPageChange: (group: TriageGroupKey, page: number) => void;
  onPageSizeChange: (group: TriageGroupKey, pageSize: number) => void;
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
    color: '#1976d2',
  },
  {
    key: 'needs_review',
    label: 'Needs Review',
    dataKey: 'needs_review',
    icon: <RateReview sx={{ fontSize: 18 }} />,
    defaultOpen: true,
    color: '#ed6c02',
    accentBorder: true,
  },
  {
    key: 'in_progress',
    label: 'In Progress',
    dataKey: 'in_progress',
    icon: <AssignmentTurnedIn sx={{ fontSize: 18 }} />,
    defaultOpen: true,
    color: '#0288d1',
  },
  {
    key: 'resolved',
    label: 'Resolved',
    dataKey: 'resolved',
    icon: <CheckCircleOutline sx={{ fontSize: 18 }} />,
    defaultOpen: false,
    color: '#2e7d32',
  },
];

export function TriageGroupedList({
  groups,
  onClaim,
  onUnclaim,
  onResolve,
  onReopen,
  onEditNote,
  onPageChange,
  onPageSizeChange,
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

  const toggleSection = (key: string) => {
    setOpenSections((prev) => {
      const next = { ...prev, [key]: !prev[key] };
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)); } catch { /* ignore */ }
      return next;
    });
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      {groups_config.map((group) => {
        const groupData = groups[group.dataKey];
        if (!groupData) return null;
        const isOpen = openSections[group.key];
        const isEmpty = groupData.count === 0;

        // Compact single-line header for empty investigating
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
              borderLeft: group.accentBorder && !isEmpty
                ? `3px solid ${group.color}`
                : undefined,
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
                  color: isEmpty ? 'text.disabled' : '#fff',
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
                  <TableContainer>
                    <Table size="small">
                      <TableHead>
                        <TableRow>
                          <TableCell sx={{ fontWeight: 600 }}>Status</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Type</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Submitted by</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Assignee</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Eval Score</TableCell>
                          <TableCell sx={{ fontWeight: 600 }}>Time</TableCell>
                          <TableCell sx={{ fontWeight: 600, width: 140, textAlign: 'right' }} />
                        </TableRow>
                      </TableHead>
                      <TableBody>
                        {groupData.sessions.map((session) => (
                          <TriageSessionRow
                            key={session.id}
                            session={session}
                            group={group.key}
                            onClaim={onClaim}
                            onUnclaim={onUnclaim}
                            onResolve={onResolve}
                            onReopen={onReopen}
                            onEditNote={onEditNote}
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
