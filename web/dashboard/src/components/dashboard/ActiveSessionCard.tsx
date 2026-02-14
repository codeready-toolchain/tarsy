/**
 * ActiveSessionCard — card showing an in-progress session with chain stage progress.
 *
 * In new TARSy all sessions are chain sessions, so this replaces the old
 * ChainProgressCard + ActiveAlertCard split.
 * Progress data comes from `session.progress` WebSocket events.
 */

import { useState, useEffect } from 'react';
import { Card, CardContent, Typography, Box, Chip, LinearProgress, Tooltip } from '@mui/material';
import { Link as LinkIcon, OpenInNew } from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { liveDuration } from '../../utils/format.ts';
import { sessionDetailPath } from '../../constants/routes.ts';
import { SESSION_STATUS } from '../../constants/sessionStatus.ts';
import type { ActiveSessionItem } from '../../types/session.ts';
import type { SessionProgressPayload } from '../../types/events.ts';

interface ActiveSessionCardProps {
  session: ActiveSessionItem;
  progress?: SessionProgressPayload;
}

export function ActiveSessionCard({ session, progress }: ActiveSessionCardProps) {
  const navigate = useNavigate();
  const [, setTick] = useState(0);

  // Tick every second so the live duration updates
  useEffect(() => {
    if (
      session.status === SESSION_STATUS.IN_PROGRESS ||
      session.status === SESSION_STATUS.CANCELLING
    ) {
      const id = setInterval(() => setTick((n) => n + 1), 1000);
      return () => clearInterval(id);
    }
  }, [session.status]);

  const totalStages = progress?.total_stages ?? 0;
  const currentIndex = progress?.current_stage_index ?? session.current_stage_index ?? 0;
  const progressPercent = totalStages > 0 ? Math.round((currentIndex / totalStages) * 100) : 0;
  const stageName = progress?.current_stage_name ?? 'Starting...';
  const statusText = progress?.status_text ?? '';

  return (
    <Card
      sx={{
        cursor: 'pointer',
        '&:hover': { boxShadow: 4 },
        border: '2px solid',
        borderColor: 'primary.main',
      }}
      onClick={() => navigate(sessionDetailPath(session.id))}
    >
      <CardContent>
        {/* Header */}
        <Box display="flex" justifyContent="space-between" alignItems="flex-start" mb={1}>
          <Box flex={1}>
            <Box display="flex" alignItems="center" gap={1} mb={0.5}>
              <Typography variant="h6" sx={{ fontWeight: 600 }}>
                {session.alert_type || 'Alert'}
              </Typography>
              <Tooltip title={`Chain: ${session.chain_id}`}>
                <Chip
                  icon={<LinkIcon />}
                  label={session.chain_id}
                  size="small"
                  color="primary"
                  variant="outlined"
                />
              </Tooltip>
            </Box>
            {session.author && (
              <Typography variant="body2" color="text.secondary">
                by {session.author}
              </Typography>
            )}
          </Box>

          <Box display="flex" alignItems="center" gap={1}>
            <StatusBadge status={session.status} />
            <Tooltip title="View Details">
              <OpenInNew fontSize="small" color="action" />
            </Tooltip>
          </Box>
        </Box>

        {/* Stage progress */}
        {totalStages > 0 && (
          <Box mb={1.5}>
            <Box display="flex" justifyContent="space-between" alignItems="center" mb={0.5}>
              <Typography variant="body2" color="text.secondary">
                {stageName}
              </Typography>
              <Typography variant="caption" color="text.secondary">
                {currentIndex}/{totalStages} stages
              </Typography>
            </Box>
            <LinearProgress
              variant="determinate"
              value={progressPercent}
              sx={{
                height: 6,
                borderRadius: 3,
                backgroundColor: 'grey.200',
                '& .MuiLinearProgress-bar': { backgroundColor: 'success.main' },
              }}
            />
          </Box>
        )}

        {/* Status text + duration */}
        <Box display="flex" justifyContent="space-between" alignItems="center">
          {statusText && (
            <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>
              {statusText}
            </Typography>
          )}
          <Typography variant="caption" color="text.secondary">
            Running for {liveDuration(session.started_at)}
          </Typography>
        </Box>
      </CardContent>
    </Card>
  );
}
