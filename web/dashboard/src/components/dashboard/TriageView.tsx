import { useState, useCallback } from 'react';
import {
  Box,
  Alert,
  Button,
  CircularProgress,
  Snackbar,
} from '@mui/material';
import { RateReview } from '@mui/icons-material';
import { TriageFilterBar } from './TriageFilterBar.tsx';
import { TriageGroupedList } from './TriageGroupedList.tsx';
import { CompleteReviewModal } from './CompleteReviewModal.tsx';
import { EditFeedbackModal } from './EditFeedbackModal.tsx';
import { getRatingConfig } from '../../constants/ratingConfig.ts';
import { getSession } from '../../services/api.ts';
import { REVIEW_MODAL_MODE, getReviewModalMode } from '../../types/api.ts';
import type { TriageGroup, TriageGroupKey, ReviewModalMode } from '../../types/api.ts';
import type { TriageFilter } from '../../types/dashboard.ts';
import type { DashboardSessionItem } from '../../types/session.ts';

interface TriageViewProps {
  groups: Record<TriageGroupKey, TriageGroup | null>;
  loading: boolean;
  error: string | null;
  filters: TriageFilter;
  onFiltersChange: (filters: TriageFilter) => void;
  onRefresh: () => void;
  onClaim: (sessionId: string) => Promise<void>;
  onUnclaim: (sessionId: string) => Promise<void>;
  onComplete: (sessionId: string, qualityRating: string, actionTaken?: string, investigationFeedback?: string) => Promise<void>;
  onReopen: (sessionId: string) => Promise<void>;
  onUpdateFeedback: (sessionId: string, qualityRating: string, actionTaken: string, investigationFeedback: string) => Promise<void>;
  onBulkClaim: (sessionIds: string[]) => Promise<void>;
  onBulkComplete: (sessionIds: string[], qualityRating: string, actionTaken?: string, investigationFeedback?: string) => Promise<void>;
  onBulkUnclaim: (sessionIds: string[]) => Promise<void>;
  onBulkReopen: (sessionIds: string[]) => Promise<void>;
  onPageChange: (group: TriageGroupKey, page: number) => void;
  onPageSizeChange: (group: TriageGroupKey, pageSize: number) => void;
}

interface SnackbarState {
  message: string;
  severity: 'success' | 'warning' | 'error';
  completedSession?: DashboardSessionItem;
  completedRating?: string;
}

export function TriageView({
  groups,
  loading,
  error,
  filters,
  onFiltersChange,
  onRefresh,
  onClaim,
  onUnclaim,
  onComplete,
  onReopen,
  onUpdateFeedback,
  onBulkClaim,
  onBulkComplete,
  onBulkUnclaim,
  onBulkReopen,
  onPageChange,
  onPageSizeChange,
}: TriageViewProps) {
  const [completeSessionIds, setCompleteSessionIds] = useState<string[] | null>(null);
  const [reviewTarget, setReviewTarget] = useState<{
    session: DashboardSessionItem;
    mode: ReviewModalMode;
  } | null>(null);
  const [actionLoading, setActionLoading] = useState(false);
  const [snackbar, setSnackbar] = useState<SnackbarState | null>(null);

  const withAction = async (fn: () => Promise<void>) => {
    setActionLoading(true);
    try {
      await fn();
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Action failed';
      setSnackbar({ message, severity: 'error' });
    } finally {
      setActionLoading(false);
    }
  };

  const handleClaim = (sessionId: string) => {
    withAction(() => onClaim(sessionId));
  };

  const handleUnclaim = (sessionId: string) => {
    withAction(() => onUnclaim(sessionId));
  };

  const handleReviewClick = useCallback((session: DashboardSessionItem) => {
    const mode = getReviewModalMode(session.review_status);
    setReviewTarget({ session, mode });
  }, []);

  const handleReviewComplete = async (qualityRating: string, actionTaken?: string, investigationFeedback?: string) => {
    if (!reviewTarget) return;
    setActionLoading(true);
    try {
      await onComplete(reviewTarget.session.id, qualityRating, actionTaken, investigationFeedback);
      const cfg = getRatingConfig(qualityRating);
      setSnackbar({
        message: `Marked as ${cfg?.label ?? qualityRating}`,
        severity: cfg?.color ?? 'success',
        completedSession: {
          ...reviewTarget.session,
          quality_rating: qualityRating,
          action_taken: actionTaken ?? reviewTarget.session.action_taken ?? null,
          investigation_feedback: investigationFeedback ?? reviewTarget.session.investigation_feedback ?? null,
        },
        completedRating: qualityRating,
      });
      setReviewTarget(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Action failed';
      setSnackbar({ message, severity: 'error' });
    } finally {
      setActionLoading(false);
    }
  };

  const handleReviewSave = async (qualityRating: string, actionTaken: string, investigationFeedback: string) => {
    if (!reviewTarget) return;
    setActionLoading(true);
    try {
      await onUpdateFeedback(reviewTarget.session.id, qualityRating, actionTaken, investigationFeedback);
      setReviewTarget(null);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Action failed';
      setSnackbar({ message, severity: 'error' });
    } finally {
      setActionLoading(false);
    }
  };

  const handleBulkCompleteConfirm = (qualityRating: string, actionTaken?: string, investigationFeedback?: string) => {
    if (!completeSessionIds) return;
    const ids = completeSessionIds;
    setCompleteSessionIds(null);
    withAction(() => onBulkComplete(ids, qualityRating, actionTaken, investigationFeedback));
  };

  const handleReopen = (sessionId: string) => {
    withAction(() => onReopen(sessionId));
  };

  const handleBulkClaim = (sessionIds: string[]) => {
    withAction(() => onBulkClaim(sessionIds));
  };

  const handleBulkComplete = (sessionIds: string[]) => {
    setCompleteSessionIds(sessionIds);
  };

  const handleBulkUnclaim = (sessionIds: string[]) => {
    withAction(() => onBulkUnclaim(sessionIds));
  };

  const handleBulkReopen = (sessionIds: string[]) => {
    withAction(() => onBulkReopen(sessionIds));
  };

  const findSessionInGroups = useCallback((sessionId: string): DashboardSessionItem | undefined => {
    for (const g of Object.values(groups)) {
      const found = g?.sessions.find(s => s.id === sessionId);
      if (found) return found;
    }
    return undefined;
  }, [groups]);

  // --- Snackbar actions (snackbar mode only) ---
  const handleSnackbarAddFeedback = async () => {
    if (!snackbar?.completedSession) return;
    const stale = snackbar.completedSession;
    setSnackbar(null);

    const fresh = findSessionInGroups(stale.id);
    if (fresh) {
      setReviewTarget({ session: fresh, mode: REVIEW_MODAL_MODE.EDIT });
      return;
    }

    try {
      const detail = await getSession(stale.id);
      setReviewTarget({
        session: {
          ...stale,
          review_status: detail.review_status,
          assignee: detail.assignee,
          quality_rating: detail.quality_rating,
          action_taken: detail.action_taken,
          investigation_feedback: detail.investigation_feedback,
          feedback_edited: detail.feedback_edited,
          feedback_edited_by: detail.feedback_edited_by,
          feedback_edited_at: detail.feedback_edited_at,
        },
        mode: REVIEW_MODAL_MODE.EDIT,
      });
    } catch {
      setReviewTarget({ session: stale, mode: REVIEW_MODAL_MODE.EDIT });
    }
  };

  const handleSnackbarUndo = () => {
    if (!snackbar?.completedSession) return;
    const sessionId = snackbar.completedSession.id;
    setSnackbar(null);
    withAction(() => onReopen(sessionId));
  };

  const hasAnyData = Object.values(groups).some(g => g !== null);
  const emptyGroups: Record<TriageGroupKey, TriageGroup | null> = {
    investigating: null, needs_review: null, in_progress: null, reviewed: null,
  };

  const completeModalTitle = completeSessionIds && completeSessionIds.length > 1
    ? `Complete Review for ${completeSessionIds.length} Sessions`
    : undefined;

  const hasSnackbarActions = snackbar?.completedSession !== undefined;

  if (error) {
    return (
      <Box sx={{ mt: 2 }}>
        <TriageFilterBar
          filters={filters}
          onFiltersChange={onFiltersChange}
          onRefresh={onRefresh}
          groups={emptyGroups}
          loading={loading}
        />
        <Alert severity="error" sx={{ mt: 1 }}>
          {error}
        </Alert>
      </Box>
    );
  }

  if (loading && !hasAnyData) {
    return (
      <Box sx={{ mt: 2 }}>
        <TriageFilterBar
          filters={filters}
          onFiltersChange={onFiltersChange}
          onRefresh={onRefresh}
          groups={emptyGroups}
          loading={loading}
        />
        <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}>
          <CircularProgress />
        </Box>
      </Box>
    );
  }

  if (!hasAnyData) return null;

  return (
    <Box sx={{ mt: 2 }}>
      <TriageFilterBar
        filters={filters}
        onFiltersChange={onFiltersChange}
        onRefresh={onRefresh}
        groups={groups}
        loading={loading}
      />

      <TriageGroupedList
        groups={groups}
        onClaim={handleClaim}
        onUnclaim={handleUnclaim}
        onReopen={handleReopen}
        onReviewClick={handleReviewClick}
        onBulkClaim={handleBulkClaim}
        onBulkComplete={handleBulkComplete}
        onBulkUnclaim={handleBulkUnclaim}
        onBulkReopen={handleBulkReopen}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
        actionLoading={actionLoading}
      />

      {/* Single-session review modals (from Review column click) */}
      <CompleteReviewModal
        open={reviewTarget?.mode === REVIEW_MODAL_MODE.COMPLETE}
        onClose={() => setReviewTarget(null)}
        onComplete={handleReviewComplete}
        loading={actionLoading}
        title={reviewTarget?.session.alert_type ? `Review: ${reviewTarget.session.alert_type}` : undefined}
        executiveSummary={reviewTarget?.session.executive_summary}
        assignee={reviewTarget?.session.assignee}
        feedbackEdited={reviewTarget?.session.feedback_edited}
        feedbackEditedBy={reviewTarget?.session.feedback_edited_by}
        feedbackEditedAt={reviewTarget?.session.feedback_edited_at}
      />
      <EditFeedbackModal
        open={reviewTarget?.mode === REVIEW_MODAL_MODE.EDIT}
        onClose={() => setReviewTarget(null)}
        onSave={handleReviewSave}
        loading={actionLoading}
        initialQualityRating={reviewTarget?.session.quality_rating ?? ''}
        initialActionTaken={reviewTarget?.session.action_taken ?? ''}
        initialInvestigationFeedback={reviewTarget?.session.investigation_feedback ?? ''}
        executiveSummary={reviewTarget?.session.executive_summary}
        assignee={reviewTarget?.session.assignee}
        feedbackEdited={reviewTarget?.session.feedback_edited}
        feedbackEditedBy={reviewTarget?.session.feedback_edited_by}
        feedbackEditedAt={reviewTarget?.session.feedback_edited_at}
      />

      {/* Bulk complete modal */}
      <CompleteReviewModal
        open={completeSessionIds !== null}
        onClose={() => setCompleteSessionIds(null)}
        onComplete={handleBulkCompleteConfirm}
        loading={actionLoading}
        title={completeModalTitle}
      />

      <Snackbar
        open={snackbar !== null}
        autoHideDuration={hasSnackbarActions ? 8000 : 4000}
        onClose={() => setSnackbar(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      >
        {snackbar ? (
          <Alert
            onClose={() => setSnackbar(null)}
            severity={snackbar.severity}
            variant="filled"
            sx={{ width: '100%' }}
            icon={(() => {
              const cfg = getRatingConfig(snackbar.completedRating);
              if (!cfg) return undefined;
              const Icon = cfg.icon;
              return <Icon fontSize="inherit" />;
            })()}
            action={hasSnackbarActions ? (
              <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
                <Button
                  color="inherit"
                  size="small"
                  variant="outlined"
                  startIcon={<RateReview sx={{ fontSize: 16 }} />}
                  onClick={handleSnackbarAddFeedback}
                  sx={{ borderColor: 'rgba(255,255,255,0.5)' }}
                >
                  Add note
                </Button>
                <Button color="inherit" size="small" onClick={handleSnackbarUndo}>
                  Undo
                </Button>
              </Box>
            ) : undefined}
          >
            {snackbar.message}
          </Alert>
        ) : undefined}
      </Snackbar>
    </Box>
  );
}
