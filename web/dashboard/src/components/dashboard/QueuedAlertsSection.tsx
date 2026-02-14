/**
 * QueuedAlertsSection — collapsible accordion showing pending sessions in queue.
 *
 * Ported from old dashboard. Adapted for new TARSy types:
 * - Uses `id` instead of `session_id`
 * - Uses RFC3339 `created_at` instead of `started_at_us` (microseconds)
 * - Uses `queue_position` from the active sessions API
 */

import { useState, useRef, useEffect } from 'react';
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Box,
  Typography,
  IconButton,
  Button,
  Chip,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogContentText,
  DialogActions,
  CircularProgress,
  Tooltip,
  List,
  ListItem,
  ListItemText,
  alpha,
} from '@mui/material';
import { ExpandMore, Schedule, CancelOutlined, OpenInNew } from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { cancelSession, handleAPIError } from '../../services/api.ts';
import { liveDuration } from '../../utils/format.ts';
import { sessionDetailPath } from '../../constants/routes.ts';
import type { QueuedSessionItem } from '../../types/session.ts';

interface QueuedAlertsSectionProps {
  sessions: QueuedSessionItem[];
  onRefresh?: () => void;
}

export function QueuedAlertsSection({ sessions, onRefresh }: QueuedAlertsSectionProps) {
  const navigate = useNavigate();
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [sessionToCancel, setSessionToCancel] = useState<string | null>(null);
  const [isCanceling, setIsCanceling] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [, forceUpdate] = useState(0);

  // Clean up timeout on unmount
  useEffect(() => {
    return () => {
      if (timeoutRef.current) clearTimeout(timeoutRef.current);
    };
  }, []);

  // Refresh wait times every 10s
  useEffect(() => {
    if (sessions.length === 0) return;
    const id = setInterval(() => forceUpdate((n) => n + 1), 10_000);
    return () => clearInterval(id);
  }, [sessions.length]);

  const handleCancelClick = (sessionId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setSessionToCancel(sessionId);
    setCancelDialogOpen(true);
    setCancelError(null);
  };

  const handleDialogClose = () => {
    if (!isCanceling) {
      setCancelDialogOpen(false);
      setSessionToCancel(null);
      setCancelError(null);
    }
  };

  const handleConfirmCancel = async () => {
    if (!sessionToCancel) return;
    setIsCanceling(true);
    setCancelError(null);
    try {
      await cancelSession(sessionToCancel);
      setCancelDialogOpen(false);
      setSessionToCancel(null);
      setIsCanceling(false);
      if (onRefresh) {
        timeoutRef.current = setTimeout(onRefresh, 500);
      }
    } catch (error) {
      setCancelError(handleAPIError(error));
      setIsCanceling(false);
    }
  };

  return (
    <>
      <Accordion
        defaultExpanded={false}
        sx={{
          backgroundColor: (theme) => alpha(theme.palette.warning.main, 0.05),
          border: '1px solid',
          borderColor: (theme) => alpha(theme.palette.warning.main, 0.2),
          '&:before': { display: 'none' },
          boxShadow: 1,
        }}
      >
        <AccordionSummary
          expandIcon={<ExpandMore />}
          sx={{
            '&:hover': {
              backgroundColor: (theme) => alpha(theme.palette.warning.main, 0.08),
            },
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%', pr: 2 }}>
            <Schedule sx={{ color: 'warning.main', fontSize: 20 }} />
            <Typography variant="h6" sx={{ fontWeight: 600 }}>
              Queued Alerts
            </Typography>
            <Chip label={sessions.length} color="warning" size="small" sx={{ fontWeight: 600 }} />
            <Typography variant="body2" color="text.secondary">
              • Expected to start soon
            </Typography>
          </Box>
        </AccordionSummary>

        <AccordionDetails sx={{ pt: 0 }}>
          <List sx={{ width: '100%', p: 0 }}>
            {sessions.map((session, index) => (
              <ListItem
                key={session.id}
                sx={{
                  borderTop: index === 0 ? 'none' : '1px solid',
                  borderColor: 'divider',
                  py: 2,
                  px: 2,
                  '&:hover': { backgroundColor: 'action.hover' },
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', width: '100%', gap: 2 }}>
                  {/* Queue position badge */}
                  <Box
                    sx={{
                      minWidth: 32,
                      height: 32,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      backgroundColor: 'warning.main',
                      color: 'white',
                      borderRadius: '50%',
                      fontWeight: 700,
                      fontSize: '0.875rem',
                    }}
                  >
                    {session.queue_position}
                  </Box>

                  {/* Alert info */}
                  <ListItemText
                    primary={
                      <Typography variant="body1" sx={{ fontWeight: 600 }}>
                        {session.alert_type || 'Unknown Alert'}
                      </Typography>
                    }
                    secondary={
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mt: 0.5 }}>
                        <Typography variant="caption" color="text.secondary">
                          Waiting: {liveDuration(session.created_at)}
                        </Typography>
                        {session.chain_id && (
                          <>
                            <Typography variant="caption" color="text.secondary">
                              •
                            </Typography>
                            <Typography variant="caption" color="text.secondary">
                              {session.chain_id}
                            </Typography>
                          </>
                        )}
                        {session.author && (
                          <>
                            <Typography variant="caption" color="text.secondary">
                              •
                            </Typography>
                            <Typography variant="caption" color="text.secondary">
                              by {session.author}
                            </Typography>
                          </>
                        )}
                      </Box>
                    }
                  />

                  {/* Action buttons */}
                  <Box sx={{ display: 'flex', gap: 1 }}>
                    <Tooltip title="View session details">
                      <Button
                        size="small"
                        variant="outlined"
                        startIcon={<OpenInNew fontSize="small" />}
                        onClick={(e) => {
                          e.stopPropagation();
                          navigate(sessionDetailPath(session.id));
                        }}
                        sx={{ textTransform: 'none', minWidth: 100 }}
                      >
                        View
                      </Button>
                    </Tooltip>

                    <Tooltip title="Cancel this queued session">
                      <IconButton
                        size="small"
                        color="error"
                        onClick={(e) => handleCancelClick(session.id, e)}
                        sx={{
                          '&:hover': {
                            backgroundColor: (theme) => alpha(theme.palette.error.main, 0.1),
                          },
                        }}
                      >
                        <CancelOutlined fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  </Box>
                </Box>
              </ListItem>
            ))}
          </List>
        </AccordionDetails>
      </Accordion>

      {/* Cancel Confirmation Dialog */}
      <Dialog open={cancelDialogOpen} onClose={handleDialogClose} maxWidth="sm" fullWidth>
        <DialogTitle>Cancel Queued Session?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            Are you sure you want to cancel this queued session? It will be removed from the queue
            and will not be processed.
          </DialogContentText>
          {cancelError && (
            <Box
              sx={{
                mt: 2,
                p: 1.5,
                bgcolor: (theme) => alpha(theme.palette.error.main, 0.05),
                borderRadius: 1,
                border: '1px solid',
                borderColor: 'error.main',
              }}
            >
              <Typography variant="body2" color="error.main">
                {cancelError}
              </Typography>
            </Box>
          )}
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button onClick={handleDialogClose} disabled={isCanceling} color="inherit">
            Keep in Queue
          </Button>
          <Button
            onClick={handleConfirmCancel}
            variant="contained"
            color="error"
            disabled={isCanceling}
            startIcon={isCanceling ? <CircularProgress size={16} color="inherit" /> : undefined}
          >
            {isCanceling ? 'Canceling...' : 'Yes, Cancel Session'}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
