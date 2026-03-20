import { useState } from 'react';
import {
  Box,
  Alert,
  Button,
  CircularProgress,
  Snackbar,
} from '@mui/material';
import { ThumbUp, ThumbsUpDown, ThumbDown, RateReview } from '@mui/icons-material';
import { TriageFilterBar } from './TriageFilterBar.tsx';
import { TriageGroupedList } from './TriageGroupedList.tsx';
import { CompleteReviewModal } from './CompleteReviewModal.tsx';
import { EditFeedbackModal } from './EditFeedbackModal.tsx';
import type { TriageGroup, TriageGroupKey } from '../../types/api.ts';
import type { TriageFilter } from '../../types/dashboard.ts';

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

interface EditFeedbackState {
  sessionId: string;
  qualityRating: string;
  actionTaken: string;
  investigationFeedback: string;
}

interface SnackbarState {
  message: string;
  severity: 'success' | 'warning' | 'error';
  completedSessionId?: string;
  completedRating?: string;
}

const RATING_CONFIG: Record<string, {
  label: string;
  severity: 'success' | 'warning' | 'error';
  icon: React.ReactElement;
}> = {
  accurate: { label: 'Accurate', severity: 'success', icon: <ThumbUp fontSize="inherit" /> },
  partially_accurate: { label: 'Partially Accurate', severity: 'warning', icon: <ThumbsUpDown fontSize="inherit" /> },
  inaccurate: { label: 'Inaccurate', severity: 'error', icon: <ThumbDown fontSize="inherit" /> },
};

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
  const [editFeedbackState, setEditFeedbackState] = useState<EditFeedbackState | null>(null);
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

  const handleComplete = async (sessionId: string, qualityRating: string) => {
    setActionLoading(true);
    try {
      await onComplete(sessionId, qualityRating);
      const cfg = RATING_CONFIG[qualityRating];
      setSnackbar({
        message: `Marked as ${cfg?.label ?? qualityRating}`,
        severity: cfg?.severity ?? 'success',
        completedSessionId: sessionId,
        completedRating: qualityRating,
      });
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

  const handleEditFeedback = (sessionId: string, qualityRating: string, actionTaken: string, investigationFeedback: string) => {
    setEditFeedbackState({ sessionId, qualityRating, actionTaken, investigationFeedback });
  };

  const handleEditFeedbackSave = (qualityRating: string, actionTaken: string, investigationFeedback: string) => {
    if (!editFeedbackState) return;
    const sessionId = editFeedbackState.sessionId;
    setEditFeedbackState(null);
    withAction(() => onUpdateFeedback(sessionId, qualityRating, actionTaken, investigationFeedback));
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

  // --- Snackbar actions (snackbar mode only) ---
  const handleSnackbarAddFeedback = () => {
    if (!snackbar?.completedSessionId) return;
    setEditFeedbackState({
      sessionId: snackbar.completedSessionId,
      qualityRating: snackbar.completedRating ?? '',
      actionTaken: '',
      investigationFeedback: '',
    });
    setSnackbar(null);
  };

  const handleSnackbarUndo = () => {
    if (!snackbar?.completedSessionId) return;
    const sessionId = snackbar.completedSessionId;
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

  const hasSnackbarActions = snackbar?.completedSessionId !== undefined;

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
        onComplete={handleComplete}
        onReopen={handleReopen}
        onEditFeedback={handleEditFeedback}
        onBulkClaim={handleBulkClaim}
        onBulkComplete={handleBulkComplete}
        onBulkUnclaim={handleBulkUnclaim}
        onBulkReopen={handleBulkReopen}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
        actionLoading={actionLoading}
      />

      <CompleteReviewModal
        open={completeSessionIds !== null}
        onClose={() => setCompleteSessionIds(null)}
        onComplete={handleBulkCompleteConfirm}
        loading={actionLoading}
        title={completeModalTitle}
      />

      <EditFeedbackModal
        open={editFeedbackState !== null}
        initialQualityRating={editFeedbackState?.qualityRating ?? ''}
        initialActionTaken={editFeedbackState?.actionTaken ?? ''}
        initialInvestigationFeedback={editFeedbackState?.investigationFeedback ?? ''}
        onClose={() => setEditFeedbackState(null)}
        onSave={handleEditFeedbackSave}
        loading={actionLoading}
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
            icon={snackbar.completedRating ? RATING_CONFIG[snackbar.completedRating]?.icon : undefined}
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
