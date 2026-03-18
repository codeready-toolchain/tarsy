import {
  TableRow,
  TableCell,
  Typography,
  Button,
  Tooltip,
  IconButton,
  Box,
  Checkbox,
} from '@mui/material';
import { Undo, StickyNote2Outlined, Check, NotInterested } from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { SummaryTooltip } from './SummaryTooltip.tsx';
import { ScoreCell } from './ScoreCell.tsx';
import { OpenNewTabButton } from './OpenNewTabButton.tsx';
import { formatTimestamp } from '../../utils/format.ts';
import { sessionDetailPath } from '../../constants/routes.ts';
import type { DashboardSessionItem } from '../../types/session.ts';

export type TriageGroup = 'investigating' | 'needs_review' | 'in_progress' | 'resolved';

interface TriageSessionRowProps {
  session: DashboardSessionItem;
  group: TriageGroup;
  selected?: boolean;
  selectionDisabled?: boolean;
  onToggleSelect?: (sessionId: string) => void;
  onClaim?: (sessionId: string) => void;
  onUnclaim?: (sessionId: string) => void;
  onResolve?: (sessionId: string) => void;
  onReopen?: (sessionId: string) => void;
  onEditNote?: (sessionId: string, currentNote: string) => void;
  actionLoading?: boolean;
}

const resolutionReasonConfig: Record<string, { label: string }> = {
  actioned: { label: 'Actioned' },
  dismissed: { label: 'Dismissed' },
};

export function TriageSessionRow({
  session,
  group,
  selected,
  selectionDisabled,
  onToggleSelect,
  onClaim,
  onUnclaim,
  onResolve,
  onReopen,
  onEditNote,
  actionLoading,
}: TriageSessionRowProps) {
  const navigate = useNavigate();

  const handleRowClick = () => {
    navigate(sessionDetailPath(session.id));
  };

  const hasActions = group !== 'investigating';
  const selectable = hasActions && onToggleSelect;

  return (
    <TableRow
      hover
      selected={selected}
      onClick={handleRowClick}
      sx={{
        cursor: 'pointer',
        '&:hover .triage-actions': { opacity: 1 },
      }}
    >
      {selectable && (
        <TableCell padding="checkbox" onClick={(e) => e.stopPropagation()}>
          <Checkbox
            size="small"
            checked={!!selected}
            disabled={!selected && selectionDisabled}
            onChange={() => onToggleSelect(session.id)}
          />
        </TableCell>
      )}
      <TableCell>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <StatusBadge status={session.status} size="small" />
          {group === 'resolved' && session.resolution_reason && (
            <Tooltip title={resolutionReasonConfig[session.resolution_reason]?.label ?? session.resolution_reason}>
              <Box
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  width: 20,
                  height: 20,
                  borderRadius: '50%',
                  border: '1px solid',
                  borderColor: session.resolution_reason === 'actioned' ? 'success.main' : 'warning.main',
                  color: session.resolution_reason === 'actioned' ? 'success.main' : 'warning.main',
                }}
              >
                {session.resolution_reason === 'actioned'
                  ? <Check sx={{ fontSize: 14 }} />
                  : <NotInterested sx={{ fontSize: 14 }} />}
              </Box>
            </Tooltip>
          )}
          <SummaryTooltip summary={session.executive_summary ?? ''} />
        </Box>
      </TableCell>

      {/* Alert type */}
      <TableCell>
        <Typography variant="body2" fontWeight={500} noWrap>
          {session.alert_type ?? '—'}
        </Typography>
      </TableCell>

      {/* Author */}
      <TableCell>
        <Typography variant="body2" color="text.secondary" noWrap>
          {session.author ?? '—'}
        </Typography>
      </TableCell>

      {/* Assignee */}
      <TableCell>
        <Typography variant="body2" color={session.assignee ? 'text.secondary' : 'text.disabled'} noWrap>
          {session.assignee ?? '—'}
        </Typography>
      </TableCell>

      {/* Eval Score */}
      <ScoreCell sessionId={session.id} score={session.latest_score} scoringStatus={session.scoring_status} />

      {/* Time */}
      <TableCell>
        <Tooltip title={formatTimestamp(session.created_at, 'absolute')}>
          <Typography variant="body2" color="text.secondary">
            {formatTimestamp(session.created_at, 'short')}
          </Typography>
        </Tooltip>
      </TableCell>

      {/* Actions */}
      <TableCell sx={{ width: 140, textAlign: 'right' }}>
        <Box
          className="triage-actions"
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 0.5,
            justifyContent: 'flex-end',
            opacity: hasActions ? 0 : 0.5,
            transition: 'opacity 0.15s',
          }}
          onClick={(e) => e.stopPropagation()}
        >
          {group === 'needs_review' && (
            <>
              <Button
                size="small"
                variant="contained"
                disabled={actionLoading}
                onClick={() => onClaim?.(session.id)}
                sx={{ textTransform: 'none', fontSize: '0.75rem', py: 0.25, px: 1.5 }}
              >
                Claim
              </Button>
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
            </>
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
              <Tooltip title={session.resolution_note || 'Add note'}>
                <IconButton
                  size="small"
                  onClick={() => onEditNote?.(session.id, session.resolution_note ?? '')}
                  sx={{
                    color: session.resolution_note ? 'primary.main' : 'text.disabled',
                  }}
                >
                  <StickyNote2Outlined sx={{ fontSize: 16 }} />
                </IconButton>
              </Tooltip>
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

          <OpenNewTabButton sessionId={session.id} />
        </Box>
      </TableCell>
    </TableRow>
  );
}
