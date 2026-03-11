import { useState } from 'react';
import {
  Box,
  Alert,
  CircularProgress,
  Snackbar,
} from '@mui/material';
import { TriageFilterBar } from './TriageFilterBar.tsx';
import { TriageGroupedList } from './TriageGroupedList.tsx';
import { ResolveModal } from './ResolveModal.tsx';
import { EditNoteModal } from './EditNoteModal.tsx';
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
  onResolve: (sessionId: string, reason: string, note?: string) => Promise<void>;
  onReopen: (sessionId: string) => Promise<void>;
  onUpdateNote: (sessionId: string, note: string) => Promise<void>;
  onPageChange: (group: TriageGroupKey, page: number) => void;
  onPageSizeChange: (group: TriageGroupKey, pageSize: number) => void;
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
  onResolve,
  onReopen,
  onUpdateNote,
  onPageChange,
  onPageSizeChange,
}: TriageViewProps) {
  const [resolveSessionId, setResolveSessionId] = useState<string | null>(null);
  const [editNoteState, setEditNoteState] = useState<{ sessionId: string; note: string } | null>(null);
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

  const handleResolveClick = (sessionId: string) => {
    setResolveSessionId(sessionId);
  };

  const handleResolveConfirm = (reason: string, note?: string) => {
    if (!resolveSessionId) return;
    const sessionId = resolveSessionId;
    setResolveSessionId(null);
    withAction(() => onResolve(sessionId, reason, note));
  };

  const handleReopen = (sessionId: string) => {
    withAction(() => onReopen(sessionId));
  };

  const handleEditNote = (sessionId: string, currentNote: string) => {
    setEditNoteState({ sessionId, note: currentNote });
  };

  const handleEditNoteSave = (note: string) => {
    if (!editNoteState) return;
    const sessionId = editNoteState.sessionId;
    setEditNoteState(null);
    withAction(() => onUpdateNote(sessionId, note));
  };

  const hasAnyData = Object.values(groups).some(g => g !== null);
  const emptyGroups: Record<TriageGroupKey, TriageGroup | null> = {
    investigating: null, needs_review: null, in_progress: null, resolved: null,
  };

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
        onResolve={handleResolveClick}
        onReopen={handleReopen}
        onEditNote={handleEditNote}
        onPageChange={onPageChange}
        onPageSizeChange={onPageSizeChange}
        actionLoading={actionLoading}
      />

      <ResolveModal
        open={resolveSessionId !== null}
        onClose={() => setResolveSessionId(null)}
        onResolve={handleResolveConfirm}
        loading={actionLoading}
      />

      <EditNoteModal
        open={editNoteState !== null}
        initialNote={editNoteState?.note ?? ''}
        onClose={() => setEditNoteState(null)}
        onSave={handleEditNoteSave}
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
