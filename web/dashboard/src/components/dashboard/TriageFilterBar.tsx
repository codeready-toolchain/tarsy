import {
  Box,
  ToggleButtonGroup,
  ToggleButton,
  IconButton,
  Tooltip,
  Typography,
  Chip,
} from '@mui/material';
import { Refresh, Wifi, WifiOff } from '@mui/icons-material';
import type { TriageFilter } from '../../types/dashboard.ts';
import type { TriageGroup, TriageGroupKey } from '../../types/api.ts';

interface TriageFilterBarProps {
  filters: TriageFilter;
  onFiltersChange: (filters: TriageFilter) => void;
  onRefresh: () => void;
  groups: Record<TriageGroupKey, TriageGroup | null>;
  loading?: boolean;
  wsConnected?: boolean;
  onRetryConnection?: () => void;
}

export function TriageFilterBar({
  filters,
  onFiltersChange,
  onRefresh,
  groups,
  loading,
  wsConnected,
  onRetryConnection,
}: TriageFilterBarProps) {
  const handleAssigneeChange = (_: React.MouseEvent<HTMLElement>, value: string | null) => {
    if (value) {
      onFiltersChange({ ...filters, assignee: value as TriageFilter['assignee'] });
    }
  };

  const totalCount = Object.values(groups).reduce((sum, g) => sum + (g?.count ?? 0), 0);
  const hasData = Object.values(groups).some(g => g !== null);

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        flexWrap: 'wrap',
        gap: 2,
        px: 0.5,
        py: 1,
      }}
    >
      <ToggleButtonGroup
        value={filters.assignee}
        exclusive
        onChange={handleAssigneeChange}
        size="small"
      >
        <ToggleButton value="all" sx={{ textTransform: 'none', px: 2 }}>
          All
        </ToggleButton>
        <ToggleButton value="mine" sx={{ textTransform: 'none', px: 2 }}>
          Mine
        </ToggleButton>
        <ToggleButton value="unassigned" sx={{ textTransform: 'none', px: 2 }}>
          Unassigned
        </ToggleButton>
      </ToggleButtonGroup>

      <Box sx={{ flexGrow: 1 }} />

      {hasData && (
        <Typography variant="body2" color="text.secondary">
          {totalCount} session{totalCount !== 1 ? 's' : ''}
        </Typography>
      )}

      {wsConnected !== undefined && (
        <Tooltip
          title={
            wsConnected
              ? 'Connected — real-time updates active'
              : 'Disconnected — click to retry connection'
          }
        >
          <Chip
            icon={
              wsConnected ? (
                <Wifi sx={{ fontSize: 16 }} />
              ) : (
                <WifiOff sx={{ fontSize: 16 }} />
              )
            }
            label={wsConnected ? 'Live' : 'Offline'}
            color={wsConnected ? 'success' : 'default'}
            size="small"
            variant={wsConnected ? 'filled' : 'outlined'}
            onClick={!wsConnected ? onRetryConnection : undefined}
          />
        </Tooltip>
      )}

      <Tooltip title="Refresh triage data">
        <span>
          <IconButton size="small" onClick={onRefresh} disabled={loading}>
            <Refresh fontSize="small" />
          </IconButton>
        </span>
      </Tooltip>
    </Box>
  );
}
