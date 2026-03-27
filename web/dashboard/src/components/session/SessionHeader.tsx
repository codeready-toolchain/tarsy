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
  IconButton,
  Collapse,
  alpha,
} from '@mui/material';
import {
  Stop,
  Replay as ReplayIcon,
  GradingOutlined,
  ExpandMore,
  SubjectRounded,
} from '@mui/icons-material';
import CopyButton from '../shared/CopyButton';
import { AlertDataContent } from './OriginalAlertCard';
import { StatusBadge } from '../common/StatusBadge';
import ProgressIndicator from '../common/ProgressIndicator';
import { formatTimestamp, formatTokensCompact } from '../../utils/format';
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

  const [alertExpanded, setAlertExpanded] = useState(false);


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

  useEffect(() => {
    if (!scoringTriggered) return;
    if (session.latest_score != null || (session.scoring_status && session.scoring_status !== 'in_progress')) {
      setScoringTriggered(false);
    }
  }, [scoringTriggered, session.latest_score, session.scoring_status]);

  return (
    <Paper
      elevation={2}
      sx={{
        px: 3,
        py: 2,
        mb: 2,
        borderRadius: 2,
        ...(isActive ? breathingGlowSx : {}),
      }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
        {/* Row 1: Title + Status + Duration + Actions */}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 2,
            flexWrap: 'wrap',
          }}
        >
          {/* Left: title + status + duration */}
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, minWidth: 0, flex: 1, flexWrap: 'wrap' }}>
            <Typography
              variant="h5"
              sx={{ fontWeight: 600, wordBreak: 'break-word' }}
            >
              {session.alert_type || 'Alert Processing'}
            </Typography>
            <StatusBadge status={session.status} />
            <Typography variant="body2" color="text.disabled">·</Typography>
            <ProgressIndicator
              status={session.status}
              startedAt={session.started_at}
              durationMs={session.duration_ms}
              variant="inline"
            />
          </Box>

          {/* Right: action buttons */}
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexShrink: 0 }}>
            {canCancel && (
              <Tooltip title={isCanceling || session.status === SESSION_STATUS.CANCELLING ? 'Canceling…' : 'Cancel session'}>
                <span>
                  <IconButton
                    onClick={handleCancelClick}
                    disabled={isCanceling || session.status === SESSION_STATUS.CANCELLING}
                    sx={{
                      color: 'error.main',
                      border: '1px solid',
                      borderColor: 'error.main',
                      backgroundColor: 'transparent',
                      transition: 'all 0.2s',
                      '&:hover': {
                        backgroundColor: 'error.main',
                        borderColor: 'error.main',
                        color: 'error.contrastText',
                        transform: 'scale(1.05)',
                      },
                      '&:disabled': {
                        borderColor: 'action.disabled',
                        color: 'action.disabled',
                        opacity: 0.5,
                      },
                    }}
                  >
                    {isCanceling || session.status === SESSION_STATUS.CANCELLING
                      ? <CircularProgress size={22} color="inherit" />
                      : <Stop />}
                  </IconButton>
                </span>
              </Tooltip>
            )}

            {isTerminal && (
              <Tooltip title="Re-submit alert">
                <IconButton
                  onClick={handleResubmit}
                  sx={{
                    color: 'info.main',
                    border: '1px solid',
                    borderColor: 'info.main',
                    backgroundColor: 'transparent',
                    transition: 'all 0.2s',
                    '&:hover': {
                      backgroundColor: 'info.main',
                      borderColor: 'info.main',
                      color: 'info.contrastText',
                      transform: 'scale(1.05)',
                    },
                  }}
                >
                  <ReplayIcon />
                </IconButton>
              </Tooltip>
            )}

            {session.status === SESSION_STATUS.COMPLETED && session.latest_score == null &&
              (!session.scoring_status || session.scoring_status === 'not_scored') && !scoringTriggered && (
              <Tooltip title={scoringError || 'Score session'}>
                <IconButton
                  size="small"
                  onClick={handleTriggerScoring}
                  sx={{ color: scoringError ? 'error.main' : 'text.secondary', '&:hover': { bgcolor: 'action.hover' } }}
                >
                  <GradingOutlined sx={{ fontSize: '1.2rem' }} />
                </IconButton>
              </Tooltip>
            )}
            {scoringTriggered && (
              <Tooltip title="Scoring in progress…">
                <span>
                  <IconButton size="small" disabled>
                    <CircularProgress size={18} color="inherit" />
                  </IconButton>
                </span>
              </Tooltip>
            )}
          </Box>
        </Box>

        {/* Row 2: Metadata line */}
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
          <Typography variant="body2" color="text.secondary">
            {formatTimestamp(session.started_at, 'absolute')}
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
          {session.runbook_url && (() => {
            let isSafeUrl = false;
            try {
              const parsed = new URL(session.runbook_url);
              isSafeUrl = parsed.protocol === 'http:' || parsed.protocol === 'https:';
            } catch { /* invalid URL */ }
            const displayText = session.runbook_url.length > 80
              ? `${session.runbook_url.substring(0, 77)}...`
              : session.runbook_url;
            return (
              <>
                <Typography variant="body2" color="text.secondary">·</Typography>
                <Typography variant="body2" color="text.secondary">
                  Runbook:{' '}
                  {isSafeUrl ? (
                    <a
                      href={session.runbook_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      style={{ color: 'inherit', textDecoration: 'underline', fontFamily: 'monospace', fontSize: '0.85em' }}
                    >
                      {displayText}
                    </a>
                  ) : (
                    <span style={{ fontFamily: 'monospace', fontSize: '0.85em' }}>{displayText}</span>
                  )}
                </Typography>
              </>
            );
          })()}
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
                      {formatTokensCompact(session.total_tokens)}
                    </Typography>
                    <Typography variant="caption" color="text.disabled">total</Typography>
                  </Box>
                  {session.input_tokens != null && (
                    <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5 }}>
                      <Typography variant="body2" sx={{ fontWeight: 600, color: 'info.main' }}>
                        {formatTokensCompact(session.input_tokens)}
                      </Typography>
                      <Typography variant="caption" color="text.disabled">in</Typography>
                    </Box>
                  )}
                  {session.output_tokens != null && (
                    <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 0.5 }}>
                      <Typography variant="body2" sx={{ fontWeight: 600, color: 'success.main' }}>
                        {formatTokensCompact(session.output_tokens)}
                      </Typography>
                      <Typography variant="caption" color="text.disabled">out</Typography>
                    </Box>
                  )}
                </Box>
              ) : <Box />}

              {/* Divider between tokens and alert data */}
              {session.total_tokens > 0 && alertData && (
                <Box sx={{ width: '1px', height: 16, bgcolor: 'divider', mx: 1 }} />
              )}

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
              <Collapse in={alertExpanded} timeout={300} mountOnEnter unmountOnExit>
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
