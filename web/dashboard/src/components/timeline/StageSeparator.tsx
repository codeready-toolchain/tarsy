import { memo, useCallback } from 'react';
import { Box, Typography, Divider, Chip, IconButton, Alert, alpha } from '@mui/material';
import type { Theme } from '@mui/material/styles';
import { Flag, ExpandMore, ExpandLess } from '@mui/icons-material';
import type { FlowItem } from '../../utils/timelineParser';
import { EXECUTION_STATUS, FAILED_EXECUTION_STATUSES, CANCELLED_EXECUTION_STATUSES } from '../../constants/sessionStatus';

interface StageSeparatorProps {
  item: FlowItem;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
}

const getStatusThemeColor = (theme: Theme, isError: boolean, isCancelled: boolean) =>
  isError ? theme.palette.error.main : isCancelled ? theme.palette.text.secondary : theme.palette.primary.main;

/**
 * StageSeparator - renders stage boundary dividers.
 * Clickable chip with expand/collapse, agent name, and error alerts.
 */
function StageSeparator({ item, isCollapsed = false, onToggleCollapse }: StageSeparatorProps) {
  const stageStatus = (item.metadata?.stage_status as string) || '';
  const isErrorStatus = FAILED_EXECUTION_STATUSES.has(stageStatus);
  const isCancelledStatus = CANCELLED_EXECUTION_STATUSES.has(stageStatus);
  // The backend prefixes stage names with the parent chain name
  // (e.g. "investigation - Synthesis"). Display only the stage-specific part.
  const rawName = item.content;
  const stageName = rawName.includes(' - ') ? rawName.split(' - ').pop()! : rawName;
  const errorMessage = (item.metadata?.error_message as string) || '';

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (onToggleCollapse && (e.key === 'Enter' || e.key === ' ' || e.key === 'Spacebar')) {
        e.preventDefault();
        onToggleCollapse();
      }
    },
    [onToggleCollapse],
  );

  return (
    <Box sx={{ my: 2.5 }}>
      <Divider sx={{ mb: 1, opacity: isCollapsed ? 0.6 : 1, transition: 'opacity 0.2s ease-in-out' }}>
        <Box
          role={onToggleCollapse ? 'button' : undefined}
          tabIndex={onToggleCollapse ? 0 : undefined}
          aria-label={onToggleCollapse ? (isCollapsed ? 'Expand stage' : 'Collapse stage') : undefined}
          onKeyDown={onToggleCollapse ? handleKeyDown : undefined}
          sx={{
            display: 'flex', alignItems: 'center', gap: 1,
            cursor: onToggleCollapse ? 'pointer' : 'default',
            borderRadius: 1, px: 1, py: 0.5,
            transition: 'all 0.2s ease-in-out',
            '&:hover': onToggleCollapse ? {
              backgroundColor: (theme: Theme) => alpha(getStatusThemeColor(theme, isErrorStatus, isCancelledStatus), 0.08),
              '& .MuiChip-root': {
                backgroundColor: (theme: Theme) => alpha(getStatusThemeColor(theme, isErrorStatus, isCancelledStatus), 0.12),
                borderColor: (theme: Theme) => getStatusThemeColor(theme, isErrorStatus, isCancelledStatus),
              }
            } : {}
          }}
          onClick={onToggleCollapse}
        >
          <Chip
            icon={<Flag />}
            label={`Stage: ${stageName}`}
            color={isErrorStatus ? 'error' : isCancelledStatus ? 'default' : 'primary'}
            variant="outlined"
            size="small"
            sx={{ fontSize: '0.8rem', fontWeight: 600, opacity: isCollapsed ? 0.8 : 1, transition: 'all 0.2s ease-in-out' }}
          />
          {onToggleCollapse && (
            <IconButton
              size="small"
              onClick={(e) => { e.stopPropagation(); onToggleCollapse(); }}
              sx={{
                padding: 0.75,
                backgroundColor: (theme: Theme) => isCollapsed ? alpha(theme.palette.text.secondary, 0.1) : alpha(getStatusThemeColor(theme, isErrorStatus, isCancelledStatus), 0.1),
                border: '1px solid',
                borderColor: (theme: Theme) => isCollapsed ? alpha(theme.palette.text.secondary, 0.2) : alpha(getStatusThemeColor(theme, isErrorStatus, isCancelledStatus), 0.2),
                color: isCollapsed ? 'text.secondary' : 'inherit',
                '&:hover': { backgroundColor: (theme: Theme) => isCollapsed ? theme.palette.text.secondary : getStatusThemeColor(theme, isErrorStatus, isCancelledStatus), color: 'white', transform: 'scale(1.1)' },
                transition: 'all 0.2s ease-in-out',
              }}
            >
              {isCollapsed ? <ExpandMore fontSize="small" /> : <ExpandLess fontSize="small" />}
            </IconButton>
          )}
        </Box>
      </Divider>
      <Typography
        variant="caption" color="text.secondary"
        sx={{ display: 'block', textAlign: 'center', fontStyle: 'italic', fontSize: '0.75rem', opacity: isCollapsed ? 0.7 : 1 }}
      >
        Agent: {(item.metadata?.agent_name as string) || stageName}
      </Typography>

      {isErrorStatus && !isCollapsed && (
        <Alert severity="error" sx={{ mt: 2, mx: 2 }}>
          <Typography variant="body2">
            <strong>
              {stageStatus === EXECUTION_STATUS.TIMED_OUT ? 'Stage Timed Out' : 'Stage Failed'}
            </strong>
            {errorMessage && `: ${errorMessage}`}
          </Typography>
        </Alert>
      )}

      {isCancelledStatus && !isCollapsed && (
        <Alert severity="info" sx={{ mt: 2, mx: 2, bgcolor: 'grey.100', '& .MuiAlert-icon': { color: 'text.secondary' } }}>
          <Typography variant="body2" color="text.secondary">
            <strong>Stage Cancelled</strong>
            {errorMessage && `: ${errorMessage}`}
          </Typography>
        </Alert>
      )}
    </Box>
  );
}

export default memo(StageSeparator);
