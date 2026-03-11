/**
 * ActiveAlertsPanel — shows active (in-progress) and queued (pending) sessions.
 *
 * Ported from old dashboard's ActiveAlertsPanel.tsx.
 * Key changes:
 * - Uses new backend ActiveSessionsResponse (separate active[] / queued[])
 * - Real-time progress via `session.progress` events (not old chain.progress)
 * - WebSocket connection indicator via `wsConnected` prop (owned by parent)
 */

import {
  Paper,
  Typography,
  Box,
  IconButton,
  CircularProgress,
  Alert,
  Stack,
  Chip,
  Tooltip,
} from '@mui/material';
import { Refresh, Wifi, WifiOff } from '@mui/icons-material';
import { ActiveSessionCard } from './ActiveSessionCard.tsx';
import { QueuedAlertsSection } from './QueuedAlertsSection.tsx';
import type { ActiveSessionItem, QueuedSessionItem } from '../../types/session.ts';
import type { SessionProgressPayload } from '../../types/events.ts';

interface ActiveAlertsPanelProps {
  activeSessions: ActiveSessionItem[];
  queuedSessions: QueuedSessionItem[];
  progressData: Record<string, SessionProgressPayload>;
  loading: boolean;
  error: string | null;
  wsConnected: boolean;
  onRefresh: () => void;
}

export function ActiveAlertsPanel({
  activeSessions,
  queuedSessions,
  progressData,
  loading,
  error,
  wsConnected,
  onRefresh,
}: ActiveAlertsPanelProps) {
  const totalCount = activeSessions.length + queuedSessions.length;
  const isInitialLoad = loading && totalCount === 0;
  const isErrorOnly = !!error && totalCount === 0;
  const isEmpty = !error && totalCount === 0;

  return (
    <Paper variant="outlined" sx={{ overflow: 'hidden', mb: 3 }}>
      {/* Panel Header */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          px: 2,
          py: 1,
          backgroundColor: 'background.default',
          borderBottom: '1px solid',
          borderColor: 'divider',
        }}
      >
        <Typography variant="subtitle2" fontWeight={600} sx={{ flexGrow: 1 }}>
          Active Alerts
        </Typography>

        {totalCount > 0 && (
          <Chip
            label={totalCount}
            color="primary"
            size="small"
            sx={{ height: 22, minWidth: 28, fontSize: '0.75rem', fontWeight: 600 }}
          />
        )}

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
        />

        <Tooltip title="Refresh alerts">
          <span>
            <IconButton size="small" onClick={onRefresh} disabled={loading} aria-label="Refresh alerts">
              {loading ? <CircularProgress size={16} /> : <Refresh fontSize="small" />}
            </IconButton>
          </span>
        </Tooltip>
      </Box>

      {/* Error */}
      {error && (
        <Alert severity="error" sx={{ mx: 2, mt: 1.5 }}>
          {error}
        </Alert>
      )}

      {isInitialLoad && (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}>
          <CircularProgress />
        </Box>
      )}

      {!isInitialLoad && isErrorOnly && null}

      {!isInitialLoad && isEmpty && (
        <Box sx={{ py: 6, textAlign: 'center' }}>
          <Typography variant="h6" color="text.secondary" gutterBottom>
            No Active Alerts
          </Typography>
          <Typography variant="body2" color="text.secondary">
            All alerts are currently completed or there are no alerts in the system.
          </Typography>
        </Box>
      )}

      {!isInitialLoad && !isErrorOnly && totalCount > 0 && (
        <Box sx={{ p: 2 }}>
          {queuedSessions.length > 0 && (
            <Box sx={{ mb: 2 }}>
              <QueuedAlertsSection sessions={queuedSessions} onRefresh={onRefresh} />
            </Box>
          )}

          {activeSessions.length > 0 && (
            <Stack spacing={2}>
              {activeSessions.map((session) => (
                <ActiveSessionCard
                  key={session.id}
                  session={session}
                  progress={progressData[session.id]}
                />
              ))}
            </Stack>
          )}

          <Box sx={{ mt: 2, pt: 2, borderTop: 1, borderColor: 'divider' }}>
            <Typography variant="body2" color="text.secondary">
              {activeSessions.length > 0 && `${activeSessions.length} active`}
              {queuedSessions.length > 0 && activeSessions.length > 0 && ' • '}
              {queuedSessions.length > 0 && `${queuedSessions.length} queued`}
              {wsConnected && ' • Live updates enabled'}
            </Typography>
          </Box>
        </Box>
      )}
    </Paper>
  );
}
