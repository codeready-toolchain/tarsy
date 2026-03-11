import {
  Box,
  Typography,
  Button,
  Chip,
  Tooltip,
  IconButton,
} from '@mui/material';
import {
  OpenInNew,
  PersonOutline,
  Undo,
} from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { formatTimestamp } from '../../utils/format.ts';
import { sessionDetailPath } from '../../constants/routes.ts';
import type { DashboardSessionItem } from '../../types/session.ts';

export type TriageGroup = 'investigating' | 'needs_review' | 'in_progress' | 'resolved';

interface TriageSessionRowProps {
  session: DashboardSessionItem;
  group: TriageGroup;
  onClaim?: (sessionId: string) => void;
  onUnclaim?: (sessionId: string) => void;
  onResolve?: (sessionId: string) => void;
  onReopen?: (sessionId: string) => void;
  actionLoading?: boolean;
}

function getAssigneeInitials(assignee: string): string {
  const parts = assignee.split('@')[0].split(/[._-]/);
  if (parts.length >= 2) {
    return (parts[0][0] + parts[1][0]).toUpperCase();
  }
  return assignee.substring(0, 2).toUpperCase();
}

const resolutionReasonLabel: Record<string, { label: string; color: 'success' | 'default' }> = {
  actioned: { label: 'Actioned', color: 'success' },
  dismissed: { label: 'Dismissed', color: 'default' },
};

export function TriageSessionRow({
  session,
  group,
  onClaim,
  onUnclaim,
  onResolve,
  onReopen,
  actionLoading,
}: TriageSessionRowProps) {
  const navigate = useNavigate();

  const handleRowClick = () => {
    navigate(sessionDetailPath(session.id));
  };

  const handleNewTab = (e: React.MouseEvent) => {
    e.stopPropagation();
    window.open(
      `${window.location.origin}${sessionDetailPath(session.id)}`,
      '_blank',
      'noopener,noreferrer',
    );
  };

  const summarySnippet = session.executive_summary
    ? session.executive_summary.length > 120
      ? session.executive_summary.substring(0, 120) + '...'
      : session.executive_summary
    : null;

  return (
    <Box
      onClick={handleRowClick}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 1.5,
        px: 2,
        py: 1.25,
        cursor: 'pointer',
        borderBottom: '1px solid',
        borderColor: 'divider',
        transition: 'background-color 0.15s',
        '&:hover': { backgroundColor: 'action.hover' },
        '&:last-child': { borderBottom: 'none' },
      }}
    >
      {/* Status badge */}
      <StatusBadge status={session.status} size="small" />

      {/* Alert type + chain */}
      <Box sx={{ minWidth: 140, flexShrink: 0 }}>
        <Typography variant="body2" fontWeight={500} noWrap>
          {session.alert_type ?? '—'}
        </Typography>
        <Typography variant="caption" color="text.secondary" noWrap>
          {session.chain_id}
        </Typography>
      </Box>

      {/* Author */}
      <Typography
        variant="body2"
        color="text.secondary"
        noWrap
        sx={{ minWidth: 80, flexShrink: 0 }}
      >
        {session.author ?? '—'}
      </Typography>

      {/* Executive summary snippet */}
      <Typography
        variant="body2"
        color="text.secondary"
        noWrap
        sx={{ flex: 1, minWidth: 0 }}
        title={session.executive_summary ?? undefined}
      >
        {summarySnippet ?? '—'}
      </Typography>

      {/* Assignee badge */}
      {session.assignee && (
        <Tooltip title={session.assignee}>
          <Chip
            icon={<PersonOutline sx={{ fontSize: 14 }} />}
            label={getAssigneeInitials(session.assignee)}
            size="small"
            variant="outlined"
            sx={{ height: 24, fontSize: '0.7rem', fontWeight: 600 }}
            onClick={(e) => e.stopPropagation()}
          />
        </Tooltip>
      )}

      {/* Time */}
      <Tooltip title={formatTimestamp(session.created_at, 'absolute')}>
        <Typography
          variant="caption"
          color="text.secondary"
          noWrap
          sx={{ minWidth: 70, textAlign: 'right', flexShrink: 0 }}
        >
          {formatTimestamp(session.created_at, 'relative')}
        </Typography>
      </Tooltip>

      {/* Action buttons */}
      <Box
        sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexShrink: 0, minWidth: 100, justifyContent: 'flex-end' }}
        onClick={(e) => e.stopPropagation()}
      >
        {group === 'needs_review' && (
          <Button
            size="small"
            variant="contained"
            disabled={actionLoading}
            onClick={() => onClaim?.(session.id)}
            sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
          >
            Claim
          </Button>
        )}

        {group === 'in_progress' && (
          <>
            <Button
              size="small"
              variant="contained"
              color="success"
              disabled={actionLoading}
              onClick={() => onResolve?.(session.id)}
              sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
            >
              Resolve
            </Button>
            <Tooltip title="Unclaim">
              <IconButton
                size="small"
                disabled={actionLoading}
                onClick={() => onUnclaim?.(session.id)}
              >
                <Undo sx={{ fontSize: 16 }} />
              </IconButton>
            </Tooltip>
          </>
        )}

        {group === 'resolved' && (
          <>
            {session.resolution_reason && (
              <Chip
                label={resolutionReasonLabel[session.resolution_reason]?.label ?? session.resolution_reason}
                color={resolutionReasonLabel[session.resolution_reason]?.color ?? 'default'}
                size="small"
                variant="outlined"
                sx={{ height: 22, fontSize: '0.7rem' }}
              />
            )}
            <Tooltip title="Reopen">
              <IconButton
                size="small"
                disabled={actionLoading}
                onClick={() => onReopen?.(session.id)}
              >
                <Undo sx={{ fontSize: 16 }} />
              </IconButton>
            </Tooltip>
          </>
        )}

        <Tooltip title="Open in new tab">
          <IconButton
            size="small"
            onClick={handleNewTab}
            sx={{ opacity: 0.5, '&:hover': { opacity: 1 } }}
          >
            <OpenInNew sx={{ fontSize: 16 }} />
          </IconButton>
        </Tooltip>
      </Box>
    </Box>
  );
}
