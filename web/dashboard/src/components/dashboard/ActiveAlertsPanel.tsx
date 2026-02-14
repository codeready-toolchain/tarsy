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
  Button,
  CircularProgress,
  Alert,
  Stack,
  Chip,
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

  return (
    <Paper sx={{ p: 3, mb: 3 }}>
      {/* Panel Header */}
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
          <Typography variant="h5" sx={{ fontWeight: 600 }}>
            Active Alerts
          </Typography>

          {totalCount > 0 && (
            <Chip label={totalCount} color="primary" size="small" sx={{ fontWeight: 600 }} />
          )}

          {/* WebSocket connection indicator */}
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
        </Box>

        <Button
          variant="outlined"
          size="small"
          startIcon={loading ? <CircularProgress size={16} /> : <Refresh />}
          onClick={onRefresh}
          disabled={loading}
        >
          {loading ? 'Loading...' : 'Refresh'}
        </Button>
      </Box>

      {/* Error */}
      {error && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      {/* Loading state */}
      {loading && totalCount === 0 ? (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}>
          <CircularProgress />
        </Box>
      ) : totalCount === 0 ? (
        /* Empty state */
        <Box sx={{ py: 6, textAlign: 'center' }}>
          <Typography variant="h6" color="text.secondary" gutterBottom>
            No Active Alerts
          </Typography>
          <Typography variant="body2" color="text.secondary">
            All alerts are completed or there are no alerts in the system.
          </Typography>
        </Box>
      ) : (
        <>
          {/* Queued Alerts Accordion */}
          {queuedSessions.length > 0 && (
            <Box sx={{ mb: 2 }}>
              <QueuedAlertsSection sessions={queuedSessions} onRefresh={onRefresh} />
            </Box>
          )}

          {/* Active Session Cards */}
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

          {/* Summary footer */}
          <Box sx={{ mt: 2, pt: 2, borderTop: 1, borderColor: 'divider' }}>
            <Typography variant="body2" color="text.secondary">
              {activeSessions.length > 0 && `${activeSessions.length} active`}
              {queuedSessions.length > 0 && activeSessions.length > 0 && ' · '}
              {queuedSessions.length > 0 && `${queuedSessions.length} queued`}
              {wsConnected && ' · Live updates enabled'}
            </Typography>
          </Box>
        </>
      )}
    </Paper>
  );
}
