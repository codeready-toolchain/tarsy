import { useState } from 'react';
import {
  Box,
  Alert,
  CircularProgress,
  Snackbar,
} from '@mui/material';
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
  const [snackbar, setSnackbar] = useState<{ message: string; severity: 'success' | 'error' } | null>(null);

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

  const handleCompleteClick = (sessionId: string) => {
    setCompleteSessionIds([sessionId]);
  };

  const handleCompleteConfirm = (qualityRating: string, actionTaken?: string, investigationFeedback?: string) => {
    if (!completeSessionIds) return;
    const ids = completeSessionIds;
    setCompleteSessionIds(null);
    if (ids.length === 1) {
      withAction(() => onComplete(ids[0], qualityRating, actionTaken, investigationFeedback));
    } else {
      withAction(() => onBulkComplete(ids, qualityRating, actionTaken, investigationFeedback));
    }
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

  const hasAnyData = Object.values(groups).some(g => g !== null);
  const emptyGroups: Record<TriageGroupKey, TriageGroup | null> = {
    investigating: null, needs_review: null, in_progress: null, reviewed: null,
  };

  const completeModalTitle = completeSessionIds && completeSessionIds.length > 1
    ? `Complete Review for ${completeSessionIds.length} Sessions`
    : undefined;

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
        onComplete={handleCompleteClick}
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
        onComplete={handleCompleteConfirm}
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
        autoHideDuration={4000}
        onClose={() => setSnackbar(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      >
        {snackbar ? (
          <Alert
            onClose={() => setSnackbar(null)}
            severity={snackbar.severity}
            variant="filled"
            sx={{ width: '100%' }}
          >
            {snackbar.message}
          </Alert>
        ) : undefined}
      </Snackbar>
    </Box>
  );
}
