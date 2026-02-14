import { memo, useCallback } from 'react';
import { Box, Typography, Divider, Chip, IconButton, Alert, alpha } from '@mui/material';
import { Flag, ExpandMore, ExpandLess } from '@mui/icons-material';
import type { FlowItem } from '../../utils/timelineParser';

interface StageSeparatorProps {
  item: FlowItem;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
}

/**
 * StageSeparator - renders stage boundary dividers.
 * Clickable chip with expand/collapse, agent name, and error alerts.
 */
function StageSeparator({ item, isCollapsed = false, onToggleCollapse }: StageSeparatorProps) {
  const stageStatus = (item.metadata?.stage_status as string) || '';
  const isErrorStatus = stageStatus === 'failed' || stageStatus === 'timed_out' || stageStatus === 'cancelled';
  const stageName = item.content;
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
              backgroundColor: alpha(isErrorStatus ? '#d32f2f' : '#1976d2', 0.08),
              '& .MuiChip-root': {
                backgroundColor: alpha(isErrorStatus ? '#d32f2f' : '#1976d2', 0.12),
                borderColor: isErrorStatus ? '#d32f2f' : '#1976d2',
              }
            } : {}
          }}
          onClick={onToggleCollapse}
        >
          <Chip
            icon={<Flag />}
            label={`Stage: ${stageName}`}
            color={isErrorStatus ? 'error' : 'primary'}
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
                backgroundColor: isCollapsed ? alpha('#666', 0.1) : alpha(isErrorStatus ? '#d32f2f' : '#1976d2', 0.1),
                border: '1px solid',
                borderColor: isCollapsed ? alpha('#666', 0.2) : alpha(isErrorStatus ? '#d32f2f' : '#1976d2', 0.2),
                color: isCollapsed ? '#666' : 'inherit',
                '&:hover': { backgroundColor: isCollapsed ? '#666' : (isErrorStatus ? '#d32f2f' : '#1976d2'), color: 'white', transform: 'scale(1.1)' },
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
              {stageStatus === 'timed_out' ? 'Stage Timed Out' : stageStatus === 'cancelled' ? 'Stage Cancelled' : 'Stage Failed'}
            </strong>
            {errorMessage && `: ${errorMessage}`}
          </Typography>
        </Alert>
      )}
    </Box>
  );
}

export default memo(StageSeparator);
