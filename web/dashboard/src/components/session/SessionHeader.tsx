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
  Collapse,
  alpha,
} from '@mui/material';
import {
  CancelOutlined,
  Replay as ReplayIcon,
  GradingOutlined,
  ExpandMore,
  SubjectRounded,
} from '@mui/icons-material';
import CopyButton from '../shared/CopyButton';
import { AlertDataContent } from './OriginalAlertCard';
import { StatusBadge } from '../common/StatusBadge';
import ProgressIndicator from '../common/ProgressIndicator';
import { formatTimestamp, formatTokens } from '../../utils/format';
import { cancelSession, triggerScoring, handleAPIError } from '../../services/api';
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

/**
 * Extract a short display name from an author string.
 * K8s service accounts like "system:serviceaccount:ns:name" → "name".
 * Everything else is returned as-is.
 */
function extractDisplayName(author: string): string {
  if (author.includes(':')) {
    return author.split(':').pop()!;
  }
  return author;
}

interface SessionHeaderProps {
  session: SessionDetailResponse;
  /** Raw alert_data string — rendered as a collapsible section inside the header card */
  alertData?: string;
}

/**
 * SessionHeader - compact banner with session identity, status, timing,
 * token usage, and cancel/resubmit/scoring actions.
 * Breathing glow applied for active sessions.
 */
export default function SessionHeader({
  session,
  alertData,
}: SessionHeaderProps) {
  const navigate = useNavigate();
  const isActive =
    ACTIVE_STATUSES.has(session.status as SessionStatus) ||
    session.status === SESSION_STATUS.PENDING;
  const canCancel = canCancelSession(session.status as SessionStatus);
  const isTerminal = isTerminalStatus(session.status as SessionStatus);

  // Alert data — collapsed by default for terminal sessions
  const [alertExpanded, setAlertExpanded] = useState(!isTerminal);


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
        slackFingerprint: session.slack_message_fingerprint || null,
      },
    });
  }, [navigate, session]);

  // Scoring
  const [scoringTriggered, setScoringTriggered] = useState(false);
  const [scoringError, setScoringError] = useState<string | null>(null);

  const handleTriggerScoring = useCallback(async () => {
    setScoringTriggered(true);
    setScoringError(null);
    try {
      await triggerScoring(session.id);
    } catch (error) {
      setScoringError(handleAPIError(error));
      setScoringTriggered(false);
    }
  }, [session.id]);

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
            {/* Title + Status */}
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
              </Typography>
              <Box sx={{ transform: 'scale(1.1)' }}>
                <StatusBadge status={session.status} />
              </Box>
            </Box>

            {/* Metadata line: timestamp + author */}
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap', mb: 0.5 }}>
              <Typography variant="body2" color="text.secondary">
                Started at {formatTimestamp(session.started_at, 'absolute')}
              </Typography>
              {session.author && (
                <>
                  <Typography variant="body2" color="text.secondary">·</Typography>
                  <Tooltip title={session.author}>
                    <Typography variant="body2" color="text.secondary">
                      by <strong>{extractDisplayName(session.author)}</strong>
                    </Typography>
                  </Tooltip>
                </>
              )}
            </Box>

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
                      backgroundColor: 'background.paper',
                      color: 'error.main',
                      borderColor: 'error.main',
                      borderWidth: 1.5,
                      transition: 'all 0.2s ease-in-out',
                      '&:hover': {
                        backgroundColor: 'error.main',
                        borderColor: 'error.main',
                        color: 'error.contrastText',
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
                      backgroundColor: 'background.paper',
                      color: 'info.main',
                      borderColor: 'info.main',
                      borderWidth: 1.5,
                      transition: 'all 0.2s ease-in-out',
                      '&:hover': {
                        backgroundColor: 'info.main',
                        borderColor: 'info.main',
                        color: 'info.contrastText',
                      },
                    }}
                  >
                    <ReplayIcon sx={{ mr: 1, fontSize: '1.2rem' }} />
                    RE-SUBMIT ALERT
                  </Button>
                </Tooltip>
              )}

              {/* Score trigger — only for completed sessions that haven't been scored */}
              {session.status === SESSION_STATUS.COMPLETED && session.latest_score == null &&
                (!session.scoring_status || session.scoring_status === 'not_scored') && !scoringTriggered && (
                <Tooltip title={scoringError || 'Run quality scoring on this session'}>
                  <Button
                    size="small"
                    variant="outlined"
                    startIcon={<GradingOutlined sx={{ fontSize: '1rem' }} />}
                    onClick={handleTriggerScoring}
                    fullWidth
                    sx={{
                      textTransform: 'none',
                      fontWeight: 500,
                      fontSize: '0.8rem',
                      py: 0.5,
                      borderRadius: '8px',
                      color: scoringError ? 'error.main' : 'text.secondary',
                      borderColor: scoringError ? 'error.main' : 'divider',
                    }}
                  >
                    Score
                  </Button>
                </Tooltip>
              )}
              {scoringTriggered && (
                <Button
                  size="small"
                  variant="outlined"
                  disabled
                  fullWidth
                  startIcon={<CircularProgress size={14} color="inherit" />}
                  sx={{ textTransform: 'none', fontWeight: 500, fontSize: '0.8rem', py: 0.5, borderRadius: '8px' }}
                >
                  Scoring…
                </Button>
              )}
            </Box>
          </Box>
        </Box>

        {/* Footer bar: token usage (left) + alert data toggle (right) */}
        {(session.total_tokens > 0 || alertData) && (
          <>
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                pt: 1.5,
                borderTop: '1px solid',
                borderColor: 'divider',
              }}
            >
              {/* Left: tokens */}
              {session.total_tokens > 0 ? (
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 3 }}>
                  <Typography variant="caption" color="text.secondary" sx={{ fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.5 }}>
                    Used tokens
                  </Typography>
                  <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5 }}>
                    <Typography variant="body2" sx={{ fontWeight: 700, color: 'warning.main' }}>
                      {formatTokens(session.total_tokens)}
                    </Typography>
                    <Typography variant="caption" color="text.disabled">total</Typography>
                  </Box>
                  {session.input_tokens != null && (
                    <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5 }}>
                      <Typography variant="body2" sx={{ fontWeight: 600, color: 'info.main' }}>
                        {formatTokens(session.input_tokens)}
                      </Typography>
                      <Typography variant="caption" color="text.disabled">in</Typography>
                    </Box>
                  )}
                  {session.output_tokens != null && (
                    <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5 }}>
                      <Typography variant="body2" sx={{ fontWeight: 600, color: 'success.main' }}>
                        {formatTokens(session.output_tokens)}
                      </Typography>
                      <Typography variant="caption" color="text.disabled">out</Typography>
                    </Box>
                  )}
                </Box>
              ) : <Box />}

              {/* Right: alert data toggle */}
              {alertData && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 0.5,
                    cursor: 'pointer',
                    ml: 2,
                  }}
                  onClick={() => setAlertExpanded(!alertExpanded)}
                >
                  <SubjectRounded sx={{ fontSize: '1rem', color: 'text.secondary' }} />
                  <Typography variant="caption" color="text.secondary" sx={{ fontWeight: 600, textTransform: 'uppercase', letterSpacing: 0.5 }}>
                    Alert data
                  </Typography>
                  <ExpandMore
                    sx={{
                      fontSize: '1rem',
                      color: 'text.disabled',
                      transition: 'transform 0.3s',
                      transform: alertExpanded ? 'rotate(180deg)' : 'rotate(0deg)',
                    }}
                  />
                  <Box onClick={(e) => e.stopPropagation()}>
                    <CopyButton text={alertData} variant="icon" size="small" tooltip="Copy raw alert data" />
                  </Box>
                </Box>
              )}
            </Box>

            {/* Expanded alert data — full width below footer bar */}
            {alertData && (
              <Collapse in={alertExpanded} timeout={300}>
                <Box sx={{ mt: 1.5 }}>
                  <AlertDataContent alertData={alertData} />
                </Box>
              </Collapse>
            )}
          </>
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
