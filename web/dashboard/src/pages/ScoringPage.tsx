/**
 * ScoringPage — dedicated view for session scoring reports.
 *
 * Shows: total score, score analysis (markdown), tool improvement report (markdown),
 * re-score button, and back link to session detail.
 */

import { useState, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  Container,
  Box,
  Paper,
  Typography,
  Button,
  Alert,
  Skeleton,
  Divider,
  Chip,
  CircularProgress,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogContentText,
  DialogActions,
} from '@mui/material';
import { alpha } from '@mui/material/styles';
import {
  ArrowBack,
  Refresh,
  GradingOutlined,
  BuildOutlined,
} from '@mui/icons-material';
import ReactMarkdown from 'react-markdown';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { ScoreBadge } from '../components/common/ScoreBadge.tsx';
import axios from 'axios';
import { getSession, getScore, triggerScoring, handleAPIError } from '../services/api.ts';
import { websocketService } from '../services/websocket.ts';
import { remarkPlugins, finalAnswerMarkdownComponents } from '../utils/markdownComponents.tsx';
import { formatTimestamp } from '../utils/format.ts';
import { sessionDetailPath } from '../constants/routes.ts';
import { EVENT_STAGE_STATUS, STAGE_TYPE } from '../constants/eventTypes.ts';
import { TERMINAL_EXECUTION_STATUSES } from '../constants/sessionStatus.ts';
import type { SessionDetailResponse } from '../types/session.ts';
import type { SessionScoreResponse } from '../types/api.ts';
import type { StageStatusPayload } from '../types/events.ts';

function ScoreHeaderSkeleton() {
  return (
    <Paper sx={{ p: 3 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
        <Skeleton variant="circular" width={60} height={60} />
        <Box sx={{ flex: 1 }}>
          <Skeleton variant="text" width="40%" height={32} />
          <Skeleton variant="text" width="25%" height={20} />
        </Box>
      </Box>
    </Paper>
  );
}

function ReportSkeleton() {
  return (
    <Paper sx={{ p: 3 }}>
      <Skeleton variant="text" width="30%" height={28} sx={{ mb: 2 }} />
      <Skeleton variant="rectangular" height={200} />
    </Paper>
  );
}

function getScoreColorKey(score: number): 'success' | 'warning' | 'error' {
  if (score >= 80) return 'success';
  if (score >= 60) return 'warning';
  return 'error';
}

export function ScoringPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const [session, setSession] = useState<SessionDetailResponse | null>(null);
  const [score, setScore] = useState<SessionScoreResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [scoreLoading, setScoreLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [scoreError, setScoreError] = useState<string | null>(null);

  const [rescoring, setRescoring] = useState(false);
  const [rescoreError, setRescoreError] = useState<string | null>(null);
  const [showRescoreDialog, setShowRescoreDialog] = useState(false);

  const loadData = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError(null);

    try {
      const sessionData = await getSession(id);
      setSession(sessionData);
    } catch (err) {
      setError(handleAPIError(err));
    } finally {
      setLoading(false);
    }
  }, [id]);

  const loadScore = useCallback(async () => {
    if (!id) return;
    setScoreLoading(true);
    setScoreError(null);

    try {
      const scoreData = await getScore(id);
      setScore(scoreData);
    } catch (err) {
      if (axios.isAxiosError(err) && err.response?.status === 404) {
        setScore(null);
      } else {
        setScoreError(handleAPIError(err));
      }
    } finally {
      setScoreLoading(false);
    }
  }, [id]);

  useEffect(() => {
    loadData();
    loadScore();
  }, [loadData, loadScore]);

  // WebSocket: re-fetch score when scoring stage completes
  useEffect(() => {
    if (!id) return;
    websocketService.connect();

    const handler = (data: Record<string, unknown>) => {
      const eventType = data.type as string | undefined;
      if (eventType !== EVENT_STAGE_STATUS) return;

      const payload = data as unknown as StageStatusPayload;
      if (payload.stage_type !== STAGE_TYPE.SCORING) return;

      // Update session stages in-place
      setSession((prev) => {
        if (!prev) return prev;
        const stages = prev.stages ?? [];
        const existing = stages.find((s) => s.id === payload.stage_id);
        if (existing) {
          return {
            ...prev,
            stages: stages.map((s) =>
              s.id === payload.stage_id ? { ...s, status: payload.status } : s,
            ),
          };
        }
        return prev;
      });

      if (TERMINAL_EXECUTION_STATUSES.has(payload.status)) {
        setRescoring(false);
        loadScore();
      }
    };

    const unsubscribe = websocketService.subscribeToChannel(`session:${id}`, handler);
    return () => { unsubscribe(); };
  }, [id, loadScore]);

  const handleRescoreClick = useCallback(() => {
    setShowRescoreDialog(true);
    setRescoreError(null);
  }, []);

  const handleRescoreDialogClose = useCallback(() => {
    if (!rescoring) {
      setShowRescoreDialog(false);
    }
  }, [rescoring]);

  const handleConfirmRescore = useCallback(async () => {
    if (!id) return;
    setRescoring(true);
    setRescoreError(null);

    try {
      await triggerScoring(id);
      setShowRescoreDialog(false);
    } catch (err) {
      setRescoreError(handleAPIError(err));
      setRescoring(false);
    }
  }, [id]);

  const headerTitle = session
    ? `Scoring - ${id?.slice(-8) ?? ''}`
    : 'Scoring';

  return (
    <>
      <Container maxWidth={false} sx={{ py: 2, px: { xs: 1, sm: 2 } }}>
        <SharedHeader title={headerTitle} showBackButton />

        <Box sx={{ mt: 2, display: 'flex', flexDirection: 'column', gap: 2 }}>
          {/* Back to session link */}
          {id && (
            <Button
              startIcon={<ArrowBack />}
              onClick={() => navigate(sessionDetailPath(id))}
              sx={{ alignSelf: 'flex-start', textTransform: 'none' }}
            >
              Back to Session
            </Button>
          )}

          {/* Loading */}
          {(loading || scoreLoading) && !session && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              <ScoreHeaderSkeleton />
              <ReportSkeleton />
              <ReportSkeleton />
            </Box>
          )}

          {/* Error */}
          {error && !loading && (
            <Alert severity="error">
              <Typography variant="body1" gutterBottom>Failed to load session</Typography>
              <Typography variant="body2">{error}</Typography>
            </Alert>
          )}

          {/* Score content */}
          {session && (
            <>
              {/* Score header card */}
              <Paper sx={{ p: 3 }}>
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 2 }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 3 }}>
                    {/* Large score number */}
                    {score?.total_score != null ? (
                      <Box
                        sx={{
                          width: 72,
                          height: 72,
                          borderRadius: '50%',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          bgcolor: (theme) => alpha(theme.palette[getScoreColorKey(score.total_score)].main, 0.1),
                          border: '3px solid',
                          borderColor: `${getScoreColorKey(score.total_score)}.main`,
                        }}
                      >
                        <Typography
                          variant="h4"
                          sx={{ fontWeight: 700, color: `${getScoreColorKey(score.total_score)}.main` }}
                        >
                          {score.total_score}
                        </Typography>
                      </Box>
                    ) : scoreLoading ? (
                      <Skeleton variant="circular" width={72} height={72} />
                    ) : (
                      <Box
                        sx={{
                          width: 72,
                          height: 72,
                          borderRadius: '50%',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          bgcolor: 'action.hover',
                          border: '3px solid',
                          borderColor: 'divider',
                        }}
                      >
                        <Typography variant="h5" color="text.secondary">—</Typography>
                      </Box>
                    )}

                    <Box>
                      <Typography variant="h5" sx={{ fontWeight: 600 }}>
                        Quality Score
                      </Typography>
                      {score ? (
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mt: 0.5, flexWrap: 'wrap' }}>
                          <ScoreBadge score={score.total_score} scoringStatus={score.status} />
                          <Typography variant="body2" color="text.secondary">
                            Triggered by: {score.score_triggered_by}
                          </Typography>
                          <Typography variant="body2" color="text.secondary">
                            {formatTimestamp(score.started_at, 'absolute')}
                          </Typography>
                          {score.prompt_hash && (
                            <Chip
                              label={`Prompt: ${score.prompt_hash.slice(0, 8)}`}
                              size="small"
                              variant="outlined"
                              sx={{ fontSize: '0.7rem' }}
                            />
                          )}
                        </Box>
                      ) : scoreError ? (
                        <Typography variant="body2" color="error.main" sx={{ mt: 0.5 }}>
                          {scoreError}
                        </Typography>
                      ) : !scoreLoading ? (
                        <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
                          No score available for this session
                        </Typography>
                      ) : null}
                    </Box>
                  </Box>

                  {/* Re-score button */}
                  <Button
                    variant="outlined"
                    startIcon={rescoring ? <CircularProgress size={16} color="inherit" /> : <Refresh />}
                    onClick={handleRescoreClick}
                    disabled={rescoring}
                    sx={{ textTransform: 'none', fontWeight: 500 }}
                  >
                    {rescoring ? 'Scoring...' : 'Re-score'}
                  </Button>
                </Box>

                {rescoreError && (
                  <Alert severity="error" sx={{ mt: 2 }}>
                    {rescoreError}
                  </Alert>
                )}
              </Paper>

              {/* Score Analysis */}
              {score?.score_analysis && (
                <Paper sx={{ p: 3 }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
                    <GradingOutlined color="primary" />
                    <Typography variant="h6" sx={{ fontWeight: 600 }}>
                      Score Analysis
                    </Typography>
                  </Box>
                  <Divider sx={{ mb: 2 }} />
                  <Box sx={{ '& pre': { overflow: 'auto' }, '& table': { borderCollapse: 'collapse' }, '& th, & td': { border: '1px solid', borderColor: 'divider', p: 1 } }}>
                    <ReactMarkdown
                      remarkPlugins={remarkPlugins}
                      components={finalAnswerMarkdownComponents}
                      skipHtml
                    >
                      {score.score_analysis}
                    </ReactMarkdown>
                  </Box>
                </Paper>
              )}

              {/* Tool Improvement Report */}
              {score?.tool_improvement_report && (
                <Paper sx={{ p: 3 }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
                    <BuildOutlined color="warning" />
                    <Typography variant="h6" sx={{ fontWeight: 600 }}>
                      Tool Improvement Report
                    </Typography>
                  </Box>
                  <Divider sx={{ mb: 2 }} />
                  <Box sx={{ '& pre': { overflow: 'auto' }, '& table': { borderCollapse: 'collapse' }, '& th, & td': { border: '1px solid', borderColor: 'divider', p: 1 } }}>
                    <ReactMarkdown
                      remarkPlugins={remarkPlugins}
                      components={finalAnswerMarkdownComponents}
                      skipHtml
                    >
                      {score.tool_improvement_report}
                    </ReactMarkdown>
                  </Box>
                </Paper>
              )}

              {/* Error details if scoring failed */}
              {score?.error_message && (
                <Alert severity="error">
                  <Typography variant="subtitle2" gutterBottom>Scoring Error</Typography>
                  <Typography variant="body2">{score.error_message}</Typography>
                </Alert>
              )}

              {/* No score and not loading */}
              {!score && !scoreLoading && !scoreError && (
                <Paper sx={{ p: 4, textAlign: 'center' }}>
                  <GradingOutlined sx={{ fontSize: 48, color: 'text.disabled', mb: 2 }} />
                  <Typography variant="h6" color="text.secondary" gutterBottom>
                    No Score Available
                  </Typography>
                  <Typography variant="body2" color="text.disabled" sx={{ mb: 2 }}>
                    This session has not been scored yet.
                  </Typography>
                  <Button
                    variant="contained"
                    onClick={handleRescoreClick}
                    disabled={rescoring}
                    startIcon={rescoring ? <CircularProgress size={16} color="inherit" /> : <GradingOutlined />}
                    sx={{ textTransform: 'none' }}
                  >
                    {rescoring ? 'Scoring...' : 'Score This Session'}
                  </Button>
                </Paper>
              )}
            </>
          )}
        </Box>
      </Container>

      {/* Re-score confirmation dialog */}
      <Dialog
        open={showRescoreDialog}
        onClose={handleRescoreDialogClose}
        maxWidth="sm"
        fullWidth
      >
        <DialogTitle>Re-score Session?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            This will run a new scoring evaluation on this session. The previous
            score will be replaced with the new result.
          </DialogContentText>
          {rescoreError && (
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
                {rescoreError}
              </Typography>
            </Box>
          )}
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button
            onClick={handleRescoreDialogClose}
            disabled={rescoring}
            color="inherit"
          >
            Cancel
          </Button>
          <Button
            onClick={handleConfirmRescore}
            variant="contained"
            color="primary"
            disabled={rescoring}
            startIcon={
              rescoring ? (
                <CircularProgress size={16} color="inherit" />
              ) : undefined
            }
          >
            {rescoring ? 'Scoring...' : 'Confirm Re-score'}
          </Button>
        </DialogActions>
      </Dialog>

      <VersionFooter />
    </>
  );
}
