/**
 * ActiveSessionCard — card showing an in-progress session with chain stage progress.
 *
 * In new TARSy all sessions are chain sessions, so this replaces the old
 * ChainProgressCard + ActiveAlertCard split.
 * Progress data comes from `session.progress` WebSocket events.
 *
 * Visual layer ported from old TARSy ActiveAlertCard.tsx:
 * - Breathing glow animation on in-progress cards
 * - Hover lift effect
 * - Indeterminate activity bar for "alive" feeling
 * - Status chip with icon (color-coded)
 * - Open-in-new-tab button
 */

import { useState, useEffect } from 'react';
import {
  Card,
  CardContent,
  Typography,
  Box,
  Chip,
  LinearProgress,
  Tooltip,
  IconButton,
} from '@mui/material';
import {
  Refresh,
  Schedule,
  OpenInNew,
} from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { liveDuration } from '../../utils/format.ts';
import { sessionDetailPath } from '../../constants/routes.ts';
import { SESSION_STATUS } from '../../constants/sessionStatus.ts';
import type { ActiveSessionItem } from '../../types/session.ts';
import type { SessionProgressPayload } from '../../types/events.ts';

// ── Status chip config (ported from old ActiveAlertCard) ──────

function getStatusChipConfig(status: string) {
  switch (status) {
    case SESSION_STATUS.IN_PROGRESS:
      return {
        color: 'info' as const,
        icon: <Refresh sx={{ fontSize: 16 }} />,
        label: 'In Progress',
      };
    case SESSION_STATUS.PENDING:
      return {
        color: 'warning' as const,
        icon: <Schedule sx={{ fontSize: 16 }} />,
        label: 'Pending',
      };
    case SESSION_STATUS.CANCELLING:
      return {
        color: 'warning' as const,
        icon: <Schedule sx={{ fontSize: 16 }} />,
        label: 'Cancelling',
      };
    default:
      return {
        color: 'default' as const,
        icon: <Schedule sx={{ fontSize: 16 }} />,
        label: status,
      };
  }
}

// ── Breathing glow animation (from old ActiveAlertCard) ───────

const breathingGlowSx = {
  '@keyframes breathingGlow': {
    '0%': {
      boxShadow:
        '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24), 0 0 8px 1px rgba(2, 136, 209, 0.2)',
    },
    '50%': {
      boxShadow:
        '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24), 0 0 24px 4px rgba(2, 136, 209, 0.45)',
    },
    '100%': {
      boxShadow:
        '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24), 0 0 8px 1px rgba(2, 136, 209, 0.2)',
    },
  },
  animation: 'breathingGlow 2.8s ease-in-out infinite',
};

// ── Component ─────────────────────────────────────────────────

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

  const statusConfig = getStatusChipConfig(session.status);
  const isActive = session.status === SESSION_STATUS.IN_PROGRESS;
  const isCancelling = session.status === SESSION_STATUS.CANCELLING;

  const handleNewTabClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    const url = `${window.location.origin}${sessionDetailPath(session.id)}`;
    window.open(url, '_blank', 'noopener,noreferrer');
  };

  return (
    <Card
      sx={{
        cursor: 'pointer',
        transition: 'all 0.2s ease-in-out',
        '&:hover': {
          transform: 'translateY(-1px)',
          boxShadow: 4,
        },
        position: 'relative',
        // Breathing glow animation for in-progress cards (from old dashboard)
        ...(isActive ? breathingGlowSx : {}),
      }}
      onClick={() => navigate(sessionDetailPath(session.id))}
    >
      <CardContent sx={{ pb: 2 }}>
        {/* Header with status chip + open-in-new-tab */}
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'flex-start',
            mb: 2,
          }}
        >
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5, flex: 1 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
              <Chip
                icon={statusConfig.icon}
                label={statusConfig.label}
                color={statusConfig.color}
                size="small"
                sx={{ fontWeight: 500 }}
              />
              <Typography variant="body2" color="text.secondary">
                {session.alert_type || 'Alert'}
              </Typography>
            </Box>
          </Box>

          {/* Open in new tab */}
          <Tooltip title="Open in new tab">
            <IconButton
              size="small"
              onClick={handleNewTabClick}
              sx={{
                opacity: 0.7,
                '&:hover': {
                  opacity: 1,
                  backgroundColor: 'action.hover',
                },
              }}
            >
              <OpenInNew fontSize="small" />
            </IconButton>
          </Tooltip>
        </Box>

        {/* Alert type title */}
        <Typography variant="h6" sx={{ fontWeight: 600, mb: 1 }}>
          {session.chain_id || session.alert_type || 'Processing'}
        </Typography>

        {/* Author + started time */}
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            mb: 2,
          }}
        >
          {session.author && (
            <Typography variant="body2" color="text.secondary">
              by {session.author}
            </Typography>
          )}
          <Typography variant="body2" color="text.secondary" sx={{ fontFamily: 'monospace' }}>
            {liveDuration(session.started_at)}
          </Typography>
        </Box>

        {/* Stage progress (chain stages — new TARSy feature) */}
        {totalStages > 0 && (
          <Box sx={{ mb: 1.5 }}>
            <Box
              sx={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                mb: 0.5,
              }}
            >
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
                '& .MuiLinearProgress-bar': { backgroundColor: 'success.main', borderRadius: 3 },
              }}
            />
          </Box>
        )}

        {/* Activity indicator — animated indeterminate bar (from old dashboard ProgressIndicator) */}
        {(isActive || isCancelling) && (
          <Box sx={{ mb: 1.5 }}>
            <Box
              sx={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                mb: 0.5,
              }}
            >
              <Typography variant="body2" color="text.secondary">
                {isActive ? 'Processing...' : 'Cancelling...'}
              </Typography>
            </Box>
            <LinearProgress
              variant="indeterminate"
              color={isCancelling ? 'warning' : 'info'}
              sx={{
                height: 6,
                borderRadius: 3,
                '& .MuiLinearProgress-bar': { borderRadius: 3 },
              }}
            />
          </Box>
        )}

        {/* Status text */}
        {statusText && (
          <Typography
            variant="caption"
            color="text.secondary"
            sx={{ fontStyle: 'italic', display: 'block' }}
          >
            {statusText}
          </Typography>
        )}
      </CardContent>
    </Card>
  );
}
