import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Paper,
  Box,
  Typography,
  Button,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogContentText,
  DialogActions,
  CircularProgress,
  Tooltip,
  alpha,
  ToggleButton,
  ToggleButtonGroup,
} from '@mui/material';
import {
  CancelOutlined,
  Replay as ReplayIcon,
  CallSplit,
  Psychology,
  AccountTree,
} from '@mui/icons-material';
import { StatusBadge } from '../common/StatusBadge';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import { formatTimestamp, formatDurationMs } from '../../utils/format';
import { cancelSession, handleAPIError } from '../../services/api';
import {
  SESSION_STATUS,
  isTerminalStatus,
  canCancelSession,
  type SessionStatus,
  ACTIVE_STATUSES,
} from '../../constants/sessionStatus';
import type { SessionDetailResponse } from '../../types/session';
import { ROUTES } from '../../constants/routes';

// --- Breathing glow for active sessions ---
const breathingGlowSx = {
  '@keyframes breathingGlow': {
    '0%': { boxShadow: '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24), 0 0 8px 1px rgba(2, 136, 209, 0.2)' },
    '50%': { boxShadow: '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24), 0 0 24px 4px rgba(2, 136, 209, 0.45)' },
    '100%': { boxShadow: '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24), 0 0 8px 1px rgba(2, 136, 209, 0.2)' },
  },
  animation: 'breathingGlow 2.8s ease-in-out infinite',
};

export type SessionView = 'reasoning' | 'trace';

interface SessionHeaderProps {
  session: SessionDetailResponse;
  view: SessionView;
  onViewChange: (view: SessionView) => void;
  /** Live duration for active sessions (ticking) */
  liveDurationMs?: number | null;
}

/**
 * SessionHeader - displays session metadata, status, token usage, MCP summary,
 * stage progress, view segmented control, and cancel/resubmit actions.
 * Breathing glow applied for active sessions.
 */
export default function SessionHeader({
  session,
  view,
  onViewChange,
  liveDurationMs,
}: SessionHeaderProps) {
  const navigate = useNavigate();
  const isActive = ACTIVE_STATUSES.has(session.status as SessionStatus) || session.status === SESSION_STATUS.PENDING;
  const canCancel = canCancelSession(session.status as SessionStatus);
  const isTerminal = isTerminalStatus(session.status as SessionStatus);

  // Cancel dialog
  const [showCancelDialog, setShowCancelDialog] = useState(false);
  const [isCanceling, setIsCanceling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);

  const handleCancelClick = useCallback(() => {
    setShowCancelDialog(true);
    setCancelError(null);
  }, []);

  const handleDialogClose = useCallback(() => {
    if (!isCanceling) {
      setShowCancelDialog(false);
      setCancelError(null);
    }
  }, [isCanceling]);

  const handleConfirmCancel = useCallback(async () => {
    setIsCanceling(true);
    setCancelError(null);
    try {
      await cancelSession(session.id);
      setShowCancelDialog(false);
    } catch (error) {
      setCancelError(handleAPIError(error));
      setIsCanceling(false);
    }
  }, [session.id]);

  // Clear canceling state when status changes
  useEffect(() => {
    if (session.status === SESSION_STATUS.CANCELLED && isCanceling) {
      setIsCanceling(false);
    }
  }, [session.status, isCanceling]);

  const handleResubmit = useCallback(() => {
    navigate(ROUTES.SUBMIT_ALERT, {
      state: {
        resubmit: true,
        alertType: session.alert_type,
        alertData: session.alert_data,
        sessionId: session.id,
        mcpSelection: session.mcp_selection || null,
      },
    });
  }, [navigate, session]);

  // Duration display
  const durationDisplay = (() => {
    if (liveDurationMs != null) return formatDurationMs(liveDurationMs);
    if (session.duration_ms != null) return formatDurationMs(session.duration_ms);
    return '—';
  })();

  // MCP summary
  const mcpServers = session.mcp_selection
    ? Object.keys(session.mcp_selection).length
    : 0;

  return (
    <Paper
      elevation={2}
      sx={{
        p: 3,
        mb: 2,
        borderRadius: 2,
        ...(isActive ? breathingGlowSx : {}),
      }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {/* Top row: title + status + actions */}
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 2, flexWrap: 'wrap' }}>
          {/* Left: Alert details */}
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 0.5, flexWrap: 'wrap' }}>
              <Typography variant="h5" sx={{ fontWeight: 600, wordBreak: 'break-word' }}>
                {session.alert_type || 'Alert Processing'}
              </Typography>
              <Box sx={{ transform: 'scale(1.1)' }}>
                <StatusBadge status={session.status} />
              </Box>
              {session.has_parallel_stages && (
                <Tooltip title={`Contains ${session.total_stages} stages with parallel agent execution`}>
                  <Box sx={(theme) => ({
                    display: 'flex', alignItems: 'center', gap: 0.5, px: 1.5, py: 0.5,
                    backgroundColor: alpha(theme.palette.secondary.main, 0.08),
                    borderRadius: '16px', border: '1px solid',
                    borderColor: alpha(theme.palette.secondary.main, 0.3),
                    cursor: 'help', transform: 'scale(1.05)',
                  })}>
                    <CallSplit sx={{ fontSize: '1.1rem', color: 'secondary.main' }} />
                    <Typography variant="body2" sx={{ fontWeight: 600, color: 'secondary.main', fontSize: '0.875rem' }}>
                      Parallel Agents
                    </Typography>
                  </Box>
                </Tooltip>
              )}
            </Box>

            <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
              Started {formatTimestamp(session.started_at, 'absolute')}
            </Typography>
            <Typography variant="caption" color="text.secondary" sx={{ fontFamily: 'monospace', fontSize: '0.75rem', opacity: 0.7 }}>
              {session.id}
            </Typography>
            {session.author && (
              <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
                Submitted by: <strong>{session.author}</strong>
              </Typography>
            )}
            {session.runbook_url && (
              <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
                Runbook: <a href={session.runbook_url} target="_blank" rel="noopener noreferrer" style={{ color: 'inherit', textDecoration: 'underline', fontFamily: 'monospace', fontSize: '0.85em' }}>
                  {session.runbook_url.length > 200 ? `${session.runbook_url.substring(0, 197)}...` : session.runbook_url}
                </a>
              </Typography>
            )}
          </Box>

          {/* Right: Duration + Actions */}
          <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 1.5, minWidth: 200 }}>
            <Typography variant="caption" sx={{ fontWeight: 600, color: isActive ? 'primary.main' : 'success.main', textTransform: 'uppercase', letterSpacing: 0.5 }}>
              Duration
            </Typography>
            <Typography variant="h5" sx={{ fontWeight: 800, color: isActive ? 'primary.main' : 'success.main' }}>
              {durationDisplay}
            </Typography>

            <Box sx={{ display: 'flex', gap: 1.5, width: '100%', mt: 1 }}>
              {canCancel && (
                <Button
                  variant="outlined" size="medium" onClick={handleCancelClick}
                  disabled={isCanceling || session.status === SESSION_STATUS.CANCELLING}
                  startIcon={isCanceling || session.status === SESSION_STATUS.CANCELLING ? <CircularProgress size={16} color="inherit" /> : <CancelOutlined />}
                  fullWidth
                  sx={{
                    textTransform: 'uppercase', fontWeight: 600, fontSize: '0.875rem', py: 1,
                    color: 'error.main', borderColor: 'error.main', borderWidth: 1.5,
                    '&:hover': { backgroundColor: 'error.main', borderColor: 'error.main', color: 'white', borderWidth: 1.5 },
                  }}
                >
                  {isCanceling || session.status === SESSION_STATUS.CANCELLING ? 'Canceling...' : 'Cancel Session'}
                </Button>
              )}
            </Box>

            {isTerminal && (
              <Tooltip title="Submit a new alert with the same data">
                <Button
                  variant="outlined" size="large" onClick={handleResubmit}
                  sx={{
                    minWidth: 180, textTransform: 'none', fontWeight: 600, fontSize: '0.95rem', py: 1, px: 2.5,
                    color: 'info.main', borderColor: 'info.main', borderWidth: 1.5,
                    '&:hover': { backgroundColor: 'info.main', borderColor: 'info.main', color: 'white' },
                  }}
                >
                  <ReplayIcon sx={{ mr: 1, fontSize: '1.2rem' }} />
                  RE-SUBMIT ALERT
                </Button>
              </Tooltip>
            )}
          </Box>
        </Box>

        {/* Summary stats row */}
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, alignItems: 'center' }}>
          <Box sx={(theme) => ({ display: 'flex', alignItems: 'center', gap: 0.5, px: 1, py: 0.5, backgroundColor: alpha(theme.palette.primary.main, 0.05), borderRadius: '16px', border: '1px solid', borderColor: alpha(theme.palette.primary.main, 0.2) })}>
            <Typography variant="body2" sx={{ fontWeight: 600, color: 'primary.main' }}>🧠 {session.llm_interaction_count}</Typography>
            <Typography variant="caption" color="primary.main">LLM</Typography>
          </Box>

          <Tooltip title={mcpServers > 0 ? `${mcpServers} MCP server(s) configured` : 'Using default MCP servers'}>
            <Box sx={(theme) => ({ display: 'flex', alignItems: 'center', gap: 0.5, px: 1, py: 0.5, backgroundColor: alpha(theme.palette.warning.main, 0.08), borderRadius: '16px', border: '1px solid', borderColor: alpha(theme.palette.warning.main, 0.3), cursor: 'pointer' })}>
              <Typography variant="body2" sx={{ fontWeight: 600, color: 'warning.main' }}>🔧 {session.mcp_interaction_count}</Typography>
              <Typography variant="caption" color="warning.main">MCP</Typography>
            </Box>
          </Tooltip>

          {session.error_message && (
            <Box sx={(theme) => ({ display: 'flex', alignItems: 'center', gap: 0.5, px: 1, py: 0.5, backgroundColor: alpha(theme.palette.error.main, 0.05), borderRadius: '16px', border: '1px solid', borderColor: alpha(theme.palette.error.main, 0.2) })}>
              <Typography variant="body2" sx={{ fontWeight: 600, color: 'error.main' }}>⚠️ Error</Typography>
            </Box>
          )}

          <Box sx={(theme) => ({ display: 'flex', alignItems: 'center', gap: 0.5, px: 1, py: 0.5, backgroundColor: alpha(theme.palette.info.main, 0.05), borderRadius: '16px', border: '1px solid', borderColor: alpha(theme.palette.info.main, 0.2) })}>
            <Typography variant="body2" sx={{ fontWeight: 600, color: 'info.main' }}>🔗 {session.completed_stages}/{session.total_stages}</Typography>
            <Typography variant="caption" color="info.main">stages</Typography>
          </Box>

          {(session.total_tokens > 0) && (
            <Box sx={(theme) => ({ display: 'flex', alignItems: 'center', gap: 0.5, px: 1, py: 0.5, backgroundColor: alpha(theme.palette.success.main, 0.05), borderRadius: '16px', border: '1px solid', borderColor: alpha(theme.palette.success.main, 0.2) })}>
              <Typography variant="body2" sx={{ fontWeight: 600, color: 'success.main' }}>🪙</Typography>
              <TokenUsageDisplay
                tokenData={{ input_tokens: session.input_tokens, output_tokens: session.output_tokens, total_tokens: session.total_tokens }}
                variant="inline" size="small"
              />
            </Box>
          )}
        </Box>

        {/* View segmented control */}
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <ToggleButtonGroup
            value={view}
            exclusive
            onChange={(_, newView) => newView && onViewChange(newView)}
            size="small"
          >
            <ToggleButton value="reasoning" sx={{ px: 2, textTransform: 'none', fontWeight: 600 }}>
              <Psychology sx={{ mr: 0.5, fontSize: 18 }} />
              Reasoning
            </ToggleButton>
            <ToggleButton value="trace" sx={{ px: 2, textTransform: 'none', fontWeight: 600 }}>
              <AccountTree sx={{ mr: 0.5, fontSize: 18 }} />
              Trace
            </ToggleButton>
          </ToggleButtonGroup>
        </Box>
      </Box>

      {/* Cancel Dialog */}
      <Dialog open={showCancelDialog} onClose={handleDialogClose} maxWidth="sm" fullWidth>
        <DialogTitle>Cancel Session?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            Are you sure you want to cancel this session? This action cannot be undone.
          </DialogContentText>
          {cancelError && (
            <Box sx={(theme) => ({ mt: 2, p: 1.5, bgcolor: alpha(theme.palette.error.main, 0.05), borderRadius: 1, border: '1px solid', borderColor: 'error.main' })}>
              <Typography variant="body2" color="error.main">{cancelError}</Typography>
            </Box>
          )}
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button onClick={handleDialogClose} disabled={isCanceling} color="inherit">Cancel</Button>
          <Button onClick={handleConfirmCancel} variant="contained" color="warning" disabled={isCanceling}
            startIcon={isCanceling ? <CircularProgress size={16} color="inherit" /> : undefined}
          >
            {isCanceling ? 'CANCELING...' : 'CONFIRM CANCELLATION'}
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
