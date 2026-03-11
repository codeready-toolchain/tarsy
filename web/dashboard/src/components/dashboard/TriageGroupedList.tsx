import { useState } from 'react';
import {
  Box,
  Paper,
  Typography,
  Chip,
  Collapse,
  IconButton,
} from '@mui/material';
import {
  ExpandMore,
  ExpandLess,
  Search as SearchIcon,
  RateReview,
  AssignmentTurnedIn,
  CheckCircleOutline,
} from '@mui/icons-material';
import { TriageSessionRow, type TriageGroup as TriageGroupName } from './TriageSessionRow.tsx';
import type { TriageResponse } from '../../types/api.ts';

interface TriageGroupedListProps {
  data: TriageResponse;
  onClaim: (sessionId: string) => void;
  onUnclaim: (sessionId: string) => void;
  onResolve: (sessionId: string) => void;
  onReopen: (sessionId: string) => void;
  actionLoading?: boolean;
}

interface GroupConfig {
  key: TriageGroupName;
  label: string;
  dataKey: keyof TriageResponse;
  icon: React.ReactElement;
  defaultOpen: boolean;
  color: string;
}

const groups: GroupConfig[] = [
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
  data,
  onClaim,
  onUnclaim,
  onResolve,
  onReopen,
  actionLoading,
}: TriageGroupedListProps) {
  const [openSections, setOpenSections] = useState<Record<string, boolean>>(() => {
    const initial: Record<string, boolean> = {};
    for (const g of groups) {
      initial[g.key] = g.defaultOpen;
    }
    return initial;
  });

  const toggleSection = (key: string) => {
    setOpenSections((prev) => ({ ...prev, [key]: !prev[key] }));
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      {groups.map((group) => {
        const groupData = data[group.dataKey];
        const isOpen = openSections[group.key];
        const isEmpty = groupData.count === 0;

        return (
          <Paper key={group.key} variant="outlined" sx={{ overflow: 'hidden' }}>
            {/* Group header */}
            <Box
              onClick={() => toggleSection(group.key)}
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 1,
                px: 2,
                py: 1.25,
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
                <Box sx={{ px: 2, py: 3, textAlign: 'center' }}>
                  <Typography variant="body2" color="text.secondary">
                    No sessions
                  </Typography>
                </Box>
              ) : (
                <>
                  {groupData.sessions.map((session) => (
                    <TriageSessionRow
                      key={session.id}
                      session={session}
                      group={group.key}
                      onClaim={onClaim}
                      onUnclaim={onUnclaim}
                      onResolve={onResolve}
                      onReopen={onReopen}
                      actionLoading={actionLoading}
                    />
                  ))}
                  {groupData.has_more && (
                    <Box sx={{ px: 2, py: 1.5, textAlign: 'center', borderTop: '1px solid', borderColor: 'divider' }}>
                      <Typography variant="caption" color="text.secondary">
                        Showing {groupData.sessions.length} of {groupData.count} resolved sessions
                      </Typography>
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
