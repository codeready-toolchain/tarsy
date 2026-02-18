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
} from '@mui/material';
import {
  CancelOutlined,
  Replay as ReplayIcon,
  CallSplit,
} from '@mui/icons-material';
import { StatusBadge } from '../common/StatusBadge';
import ProgressIndicator from '../common/ProgressIndicator';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import { formatTimestamp } from '../../utils/format';
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

// --- Pulse animation for "Live Processing" dot ---
const liveDotPulseSx = {
  '@keyframes liveDotPulse': {
    '0%, 100%': { opacity: 0.4 },
    '50%': { opacity: 1 },
  },
  animation: 'liveDotPulse 2s ease-in-out infinite',
};

interface SessionHeaderProps {
  session: SessionDetailResponse;
}

/**
 * SessionHeader - displays session metadata, status, token usage, MCP summary,
 * stage progress, view segmented control, and cancel/resubmit actions.
 * Breathing glow applied for active sessions.
 */
export default function SessionHeader({
  session,
}: SessionHeaderProps) {
  const navigate = useNavigate();
  const isActive =
    ACTIVE_STATUSES.has(session.status as SessionStatus) ||
    session.status === SESSION_STATUS.PENDING;
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
      setIsCanceling(false);
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
        runbook: session.runbook_url || null,
        mcpSelection: session.mcp_selection || null,
      },
    });
  }, [navigate, session]);

  // MCP summary
  const mcpServers = session.mcp_selection
    ? Object.keys(session.mcp_selection).length
    : 0;

  // Total interactions
  const totalInteractions =
    (session.llm_interaction_count ?? 0) + (session.mcp_interaction_count ?? 0);

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
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'flex-start',
            gap: 2,
            flexWrap: 'wrap',
          }}
        >
          {/* Left: Alert details */}
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 2,
                mb: 0.5,
                flexWrap: 'wrap',
              }}
            >
              <Typography
                variant="h5"
                sx={{ fontWeight: 600, wordBreak: 'break-word' }}
              >
                {session.alert_type || 'Alert Processing'}
                {session.chain_id && (
                  <Typography component="span" sx={{ color: 'text.secondary', fontWeight: 400 }}>
                    {' - '}{session.chain_id}
                  </Typography>
                )}
              </Typography>
              <Box sx={{ transform: 'scale(1.1)' }}>
                <StatusBadge status={session.status} />
              </Box>
              {session.has_parallel_stages && (
                <Tooltip
                  title={`Contains ${session.total_stages} stages with parallel agent execution`}
                >
                  <Box
                    sx={(theme) => ({
                      display: 'flex',
                      alignItems: 'center',
                      gap: 0.5,
                      px: 1.5,
                      py: 0.5,
                      backgroundColor: alpha(
                        theme.palette.secondary.main,
                        0.08,
                      ),
                      borderRadius: '16px',
                      border: '1px solid',
                      borderColor: alpha(
                        theme.palette.secondary.main,
                        0.3,
                      ),
                      cursor: 'pointer',
                      transform: 'scale(1.05)',
                      transition: 'all 0.2s ease-in-out',
                      '&:hover': {
                        backgroundColor: alpha(
                          theme.palette.secondary.main,
                          0.15,
                        ),
                        borderColor: theme.palette.secondary.main,
                      },
                    })}
                  >
                    <CallSplit
                      sx={{ fontSize: '1.1rem', color: 'secondary.main' }}
                    />
                    <Typography
                      variant="body2"
                      sx={{
                        fontWeight: 600,
                        color: 'secondary.main',
                        fontSize: '0.875rem',
                      }}
                    >
                      Parallel Agents
                    </Typography>
                  </Box>
                </Tooltip>
              )}
            </Box>

            <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
              Started at {formatTimestamp(session.started_at, 'absolute')}
            </Typography>
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{
                fontFamily: 'monospace',
                fontSize: '0.75rem',
                opacity: 0.7,
              }}
            >
              {session.id}
            </Typography>
            {session.author && (
              <Typography
                variant="body2"
                color="text.secondary"
                sx={{ mt: 0.5 }}
              >
                Submitted by: <strong>{session.author}</strong>
              </Typography>
            )}
            {session.runbook_url && (() => {
              let isSafeUrl = false;
              try {
                const parsed = new URL(session.runbook_url);
                isSafeUrl = parsed.protocol === 'http:' || parsed.protocol === 'https:';
              } catch { /* invalid URL */ }
              const displayText = session.runbook_url.length > 200
                ? `${session.runbook_url.substring(0, 197)}...`
                : session.runbook_url;
              return (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 0.5,
                    mt: 0.5,
                  }}
                >
                  <Typography variant="body2" color="text.secondary">
                    Runbook:{' '}
                    {isSafeUrl ? (
                      <a
                        href={session.runbook_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        style={{
                          color: 'inherit',
                          textDecoration: 'underline',
                          fontFamily: 'monospace',
                          fontSize: '0.85em',
                        }}
                      >
                        {displayText}
                      </a>
                    ) : (
                      <span style={{ fontFamily: 'monospace', fontSize: '0.85em' }}>
                        {displayText}
                      </span>
                    )}
                  </Typography>
                </Box>
              );
            })()}
          </Box>

          {/* Right: Duration + Actions */}
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-end',
              gap: 1.5,
              minWidth: 200,
            }}
          >
            <ProgressIndicator
              status={session.status}
              startedAt={session.started_at}
              durationMs={session.duration_ms}
            />

            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                gap: 1,
                width: '100%',
                mt: 1,
              }}
            >
              {canCancel && (
                <Tooltip title="Cancels entire session including all agents">
                  <Button
                    variant="outlined"
                    size="medium"
                    onClick={handleCancelClick}
                    disabled={
                      isCanceling ||
                      session.status === SESSION_STATUS.CANCELLING
                    }
                    startIcon={
                      isCanceling ||
                      session.status === SESSION_STATUS.CANCELLING ? (
                        <CircularProgress size={16} color="inherit" />
                      ) : (
                        <CancelOutlined />
                      )
                    }
                    fullWidth
                    sx={{
                      textTransform: 'uppercase',
                      fontWeight: 600,
                      fontSize: '0.875rem',
                      py: 1,
                      px: 2,
                      backgroundColor: 'white',
                      color: 'error.main',
                      borderColor: 'error.main',
                      borderWidth: 1.5,
                      transition: 'all 0.2s ease-in-out',
                      '&:hover': {
                        backgroundColor: 'error.main',
                        borderColor: 'error.main',
                        color: 'white',
                        borderWidth: 1.5,
                      },
                    }}
                  >
                    {isCanceling ||
                    session.status === SESSION_STATUS.CANCELLING
                      ? 'Canceling...'
                      : 'Cancel Session'}
                  </Button>
                </Tooltip>
              )}

              {isTerminal && (
                <Tooltip title="Submit a new alert with the same data">
                  <Button
                    variant="outlined"
                    size="large"
                    onClick={handleResubmit}
                    sx={{
                      minWidth: 180,
                      textTransform: 'none',
                      fontWeight: 600,
                      fontSize: '0.95rem',
                      py: 1,
                      px: 2.5,
                      backgroundColor: 'white',
                      color: 'info.main',
                      borderColor: 'info.main',
                      borderWidth: 1.5,
                      transition: 'all 0.2s ease-in-out',
                      '&:hover': {
                        backgroundColor: 'info.main',
                        borderColor: 'info.main',
                        color: 'white',
                      },
                    }}
                  >
                    <ReplayIcon sx={{ mr: 1, fontSize: '1.2rem' }} />
                    RE-SUBMIT ALERT
                  </Button>
                </Tooltip>
              )}
            </Box>
          </Box>
        </Box>

        {/* Session Summary header */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          <Typography
            variant="subtitle2"
            sx={{
              fontWeight: 700,
              fontSize: '0.85rem',
              color: 'text.secondary',
              letterSpacing: 0.3,
            }}
          >
            📊 Session Summary
          </Typography>
          {isActive && (
            <Box
              sx={(theme) => ({
                display: 'inline-flex',
                alignItems: 'center',
                gap: 0.75,
                bgcolor: alpha(theme.palette.info.main, 0.05),
                border: '1px solid',
                borderColor: alpha(theme.palette.info.main, 0.2),
                borderRadius: '12px',
                px: 1.25,
                py: 0.25,
              })}
            >
              <Box
                sx={{
                  width: 6,
                  height: 6,
                  borderRadius: '50%',
                  bgcolor: 'info.main',
                  ...liveDotPulseSx,
                }}
              />
              <Typography variant="caption" sx={{ color: 'info.main', fontWeight: 600 }}>
                Live Processing
              </Typography>
            </Box>
          )}
        </Box>

        {/* Summary stats row */}
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: 1,
            alignItems: 'center',
          }}
        >
          {/* Total interactions badge */}
          <Box
            sx={(theme) => ({
              display: 'flex',
              alignItems: 'center',
              gap: 0.5,
              px: 1,
              py: 0.5,
              backgroundColor: alpha(theme.palette.grey[500], 0.08),
              borderRadius: '16px',
              border: '1px solid',
              borderColor: alpha(theme.palette.grey[500], 0.2),
            })}
          >
            <Typography
              variant="body2"
              sx={{ fontWeight: 600, color: 'text.primary' }}
            >
              {totalInteractions}
            </Typography>
            <Typography variant="caption" color="text.secondary">
              total
            </Typography>
          </Box>

          <Box
            sx={(theme) => ({
              display: 'flex',
              alignItems: 'center',
              gap: 0.5,
              px: 1,
              py: 0.5,
              backgroundColor: alpha(theme.palette.primary.main, 0.05),
              borderRadius: '16px',
              border: '1px solid',
              borderColor: alpha(theme.palette.primary.main, 0.2),
            })}
          >
            <Typography
              variant="body2"
              sx={{ fontWeight: 600, color: 'primary.main' }}
            >
              🧠 {session.llm_interaction_count ?? 0}
            </Typography>
            <Typography variant="caption" color="primary.main">
              LLM
            </Typography>
          </Box>

          <Tooltip
            title={
              mcpServers > 0 ? (
                <Box>
                  <Typography variant="caption" sx={{ fontWeight: 700, display: 'block', mb: 0.5 }}>
                    MCP Communications
                  </Typography>
                  {session.mcp_selection && Object.entries(session.mcp_selection).map(([serverName, serverData]) => {
                    const tools = Array.isArray(serverData) ? serverData : (serverData as Record<string, unknown>)?.tools;
                    const toolCount = Array.isArray(tools) ? tools.length : 0;
                    return (
                      <Typography key={serverName} variant="caption" sx={{ display: 'block' }}>
                        {serverName}: {toolCount} tool{toolCount !== 1 ? 's' : ''}
                      </Typography>
                    );
                  })}
                </Box>
              ) : (
                'Using default MCP servers'
              )
            }
          >
            <Box
              sx={(theme) => ({
                display: 'flex',
                alignItems: 'center',
                gap: 0.5,
                px: 1,
                py: 0.5,
                backgroundColor: alpha(theme.palette.warning.main, 0.08),
                borderRadius: '16px',
                border: '1px solid',
                borderColor: alpha(theme.palette.warning.main, 0.3),
                cursor: 'pointer',
                transition: 'all 0.2s ease-in-out',
                '&:hover': {
                  backgroundColor: alpha(theme.palette.warning.main, 0.16),
                  borderColor: alpha(theme.palette.warning.main, 0.5),
                },
              })}
            >
              <Typography
                variant="body2"
                sx={{ fontWeight: 600, color: 'warning.main' }}
              >
                🔧 {session.mcp_interaction_count ?? 0}
              </Typography>
              <Typography variant="caption" color="warning.main">
                MCP
              </Typography>
            </Box>
          </Tooltip>

          {session.error_message && (
            <Box
              sx={(theme) => ({
                display: 'flex',
                alignItems: 'center',
                gap: 0.5,
                px: 1,
                py: 0.5,
                backgroundColor: alpha(theme.palette.error.main, 0.05),
                borderRadius: '16px',
                border: '1px solid',
                borderColor: alpha(theme.palette.error.main, 0.2),
              })}
            >
              <Typography
                variant="body2"
                sx={{ fontWeight: 600, color: 'error.main' }}
              >
                ⚠️ 1
              </Typography>
              <Typography variant="caption" color="error.main">
                errors
              </Typography>
            </Box>
          )}

          <Box
            sx={(theme) => ({
              display: 'flex',
              alignItems: 'center',
              gap: 0.5,
              px: 1,
              py: 0.5,
              backgroundColor: alpha(theme.palette.info.main, 0.05),
              borderRadius: '16px',
              border: '1px solid',
              borderColor: alpha(theme.palette.info.main, 0.2),
            })}
          >
            <Typography
              variant="body2"
              sx={{ fontWeight: 600, color: 'info.main' }}
            >
              🔗 {session.completed_stages}/{session.total_stages}
            </Typography>
            <Typography variant="caption" color="info.main">
              stages
            </Typography>
          </Box>

          {session.total_tokens > 0 && (
            <Box
              sx={(theme) => ({
                display: 'flex',
                alignItems: 'center',
                gap: 0.5,
                px: 1,
                py: 0.5,
                backgroundColor: alpha(
                  theme.palette.success.main,
                  0.05,
                ),
                borderRadius: '16px',
                border: '1px solid',
                borderColor: alpha(
                  theme.palette.success.main,
                  0.2,
                ),
              })}
            >
              <Typography
                variant="body2"
                sx={{ fontWeight: 600, color: 'success.main' }}
              >
                🪙
              </Typography>
              <TokenUsageDisplay
                tokenData={{
                  input_tokens: session.input_tokens,
                  output_tokens: session.output_tokens,
                  total_tokens: session.total_tokens,
                }}
                variant="inline"
                size="small"
              />
            </Box>
          )}
        </Box>

        {/* Detailed token usage block */}
        {session.total_tokens > 0 && (
          <Box sx={{ mt: 0.5 }}>
            <TokenUsageDisplay
              tokenData={{
                input_tokens: session.input_tokens,
                output_tokens: session.output_tokens,
                total_tokens: session.total_tokens,
              }}
              variant="detailed"
              size="medium"
              showBreakdown
              label="Session Token Usage"
              color="success"
            />
          </Box>
        )}

      </Box>

      {/* Cancel Dialog */}
      <Dialog
        open={showCancelDialog}
        onClose={handleDialogClose}
        maxWidth="sm"
        fullWidth
      >
        <DialogTitle>Cancel Session?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            Are you sure you want to cancel this session? This action cannot be
            undone. The session will be marked as cancelled and any ongoing
            processing will be stopped.
          </DialogContentText>
          {cancelError && (
            <Box
              sx={(theme) => ({
                mt: 2,
                p: 1.5,
                bgcolor: alpha(theme.palette.error.main, 0.05),
                borderRadius: 1,
                border: '1px solid',
                borderColor: 'error.main',
              })}
            >
              <Typography variant="body2" color="error.main">
                {cancelError}
              </Typography>
            </Box>
          )}
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button
            onClick={handleDialogClose}
            disabled={isCanceling}
            color="inherit"
          >
            Cancel
          </Button>
          <Button
            onClick={handleConfirmCancel}
            variant="contained"
            color="warning"
            disabled={isCanceling}
            startIcon={
              isCanceling ? (
                <CircularProgress size={16} color="inherit" />
              ) : undefined
            }
          >
            {isCanceling ? 'CANCELING...' : 'CONFIRM CANCELLATION'}
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}
