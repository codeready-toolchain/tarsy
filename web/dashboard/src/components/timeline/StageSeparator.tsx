import { memo, useCallback } from 'react';
import { Box, Typography, Divider, Alert } from '@mui/material';
import {
  Search, ExpandMore, ExpandLess,
  MergeType, SmsOutlined, AutoAwesome, BuildOutlined, GradingOutlined,
} from '@mui/icons-material';
import type { FlowItem } from '../../utils/timelineParser';
import { EXECUTION_STATUS, FAILED_EXECUTION_STATUSES, CANCELLED_EXECUTION_STATUSES } from '../../constants/sessionStatus';
import { STAGE_TYPE } from '../../constants/eventTypes';
import ErrorCard from './ErrorCard';

interface StageSeparatorProps {
  item: FlowItem;
  isCollapsed?: boolean;
  onToggleCollapse?: () => void;
}

function getStageTypeIcon(stageType: string | undefined) {
  const sx = { fontSize: 16 };
  switch (stageType) {
    case STAGE_TYPE.SYNTHESIS: return <MergeType sx={sx} />;
    case STAGE_TYPE.CHAT: return <SmsOutlined sx={sx} />;
    case STAGE_TYPE.EXEC_SUMMARY: return <AutoAwesome sx={sx} />;
    case STAGE_TYPE.ACTION: return <BuildOutlined sx={sx} />;
    case STAGE_TYPE.SCORING: return <GradingOutlined sx={sx} />;
    default: return <Search sx={sx} />;
  }
}

/**
 * StageSeparator — minimal stage boundary divider.
 * A single clickable line: icon + stage name + chevron.
 */
function StageSeparator({ item, isCollapsed = false, onToggleCollapse }: StageSeparatorProps) {
  const stageStatus = (item.metadata?.stage_status as string) || '';
  const stageType = item.metadata?.stage_type as string | undefined;
  const isErrorStatus = FAILED_EXECUTION_STATUSES.has(stageStatus);
  const isCancelledStatus = CANCELLED_EXECUTION_STATUSES.has(stageStatus);
  const rawName = item.content;
  const stageName = rawName.includes(' - ') ? rawName.split(' - ').pop()! : rawName;
  const errorMessage = (item.metadata?.error_message as string) || '';

  const hoverColor = isErrorStatus ? 'error.main' : 'primary.main';

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
    <Box sx={{ my: 1.5 }}>
      <Divider
        role={onToggleCollapse ? 'button' : undefined}
        tabIndex={onToggleCollapse ? 0 : undefined}
        aria-label={onToggleCollapse ? (isCollapsed ? 'Expand stage' : 'Collapse stage') : undefined}
        onKeyDown={onToggleCollapse ? handleKeyDown : undefined}
        onClick={onToggleCollapse}
        sx={{
          cursor: onToggleCollapse ? 'pointer' : 'default',
          opacity: isCollapsed ? 0.6 : 1,
          transition: 'opacity 0.2s',
          '&:hover': onToggleCollapse ? { opacity: 1, '& .stage-label': { color: hoverColor } } : {},
          '&::before, &::after': { borderColor: 'divider' },
        }}
      >
        <Box
          className="stage-label"
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 0.5,
            color: isErrorStatus ? 'error.main' : 'text.disabled',
            fontSize: '0.75rem',
            fontWeight: 600,
            textTransform: 'uppercase',
            letterSpacing: 1,
            whiteSpace: 'nowrap',
            transition: 'color 0.2s',
          }}
        >
          {getStageTypeIcon(stageType)}
          {stageName}
          {onToggleCollapse && (
            isCollapsed
              ? <ExpandMore sx={{ fontSize: 18, ml: 0.25, transition: 'transform 0.3s' }} />
              : <ExpandLess sx={{ fontSize: 18, ml: 0.25, transition: 'transform 0.3s' }} />
          )}
        </Box>
      </Divider>

      {isErrorStatus && !isCollapsed && (
        <ErrorCard
          label={stageStatus === EXECUTION_STATUS.TIMED_OUT ? 'Stage Timed Out' : 'Stage Failed'}
          message={errorMessage}
          sx={{ mt: 2, mx: 2 }}
        />
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
