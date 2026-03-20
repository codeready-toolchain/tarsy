import {
  TableRow,
  TableCell,
  Typography,
  Button,
  Tooltip,
  IconButton,
  Box,
  Checkbox,
  Divider,
} from '@mui/material';
import { PersonRemove, Replay, EditNote, ThumbUp, ThumbsUpDown, ThumbDown } from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { SummaryTooltip } from './SummaryTooltip.tsx';
import { ScoreCell } from './ScoreCell.tsx';
import { OpenNewTabButton } from './OpenNewTabButton.tsx';
import { formatTimestamp } from '../../utils/format.ts';
import { sessionDetailPath } from '../../constants/routes.ts';
import type { DashboardSessionItem } from '../../types/session.ts';

export type TriageGroup = 'investigating' | 'needs_review' | 'in_progress' | 'reviewed';

interface TriageSessionRowProps {
  session: DashboardSessionItem;
  group: TriageGroup;
  selected?: boolean;
  selectionDisabled?: boolean;
  onToggleSelect?: (sessionId: string) => void;
  onClaim?: (sessionId: string) => void;
  onUnclaim?: (sessionId: string) => void;
  onComplete?: (sessionId: string, qualityRating: string) => void;
  onReopen?: (sessionId: string) => void;
  onEditFeedback?: (sessionId: string, qualityRating: string, actionTaken: string, investigationFeedback: string) => void;
  actionLoading?: boolean;
}

const qualityRatingConfig: Record<string, { label: string; color: string; icon: React.ReactElement }> = {
  accurate: { label: 'Accurate', color: 'success.main', icon: <ThumbUp sx={{ fontSize: 14 }} /> },
  partially_accurate: { label: 'Partially Accurate', color: 'warning.main', icon: <ThumbsUpDown sx={{ fontSize: 14 }} /> },
  inaccurate: { label: 'Inaccurate', color: 'error.main', icon: <ThumbDown sx={{ fontSize: 14 }} /> },
};

export function TriageSessionRow({
  session,
  group,
  selected,
  selectionDisabled,
  onToggleSelect,
  onClaim,
  onUnclaim,
  onComplete,
  onReopen,
  onEditFeedback,
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
          {group === 'reviewed' && session.quality_rating && (() => {
            const cfg = qualityRatingConfig[session.quality_rating];
            if (!cfg) return null;
            return (
              <Tooltip title={cfg.label}>
                <Box
                  sx={{
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    width: 20,
                    height: 20,
                    borderRadius: '50%',
                    border: '1px solid',
                    borderColor: cfg.color,
                    color: cfg.color,
                  }}
                >
                  {cfg.icon}
                </Box>
              </Tooltip>
            );
          })()}
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
      <TableCell sx={{ width: 180, textAlign: 'right' }}>
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
                variant="outlined"
                disabled={actionLoading}
                onClick={() => onClaim?.(session.id)}
                sx={{ textTransform: 'none', fontSize: '0.7rem', py: 0.125, px: 1, minWidth: 'auto', lineHeight: 1.5 }}
              >
                Claim
              </Button>
              <Divider orientation="vertical" flexItem sx={{ mx: 0.25 }} />
              <Tooltip title="Accurate">
                <IconButton size="small" disabled={actionLoading} onClick={() => onComplete?.(session.id, 'accurate')} sx={{ color: 'success.main', p: 0.5 }}>
                  <ThumbUp sx={{ fontSize: 18 }} />
                </IconButton>
              </Tooltip>
              <Tooltip title="Partially Accurate">
                <IconButton size="small" disabled={actionLoading} onClick={() => onComplete?.(session.id, 'partially_accurate')} sx={{ color: 'warning.main', p: 0.5 }}>
                  <ThumbsUpDown sx={{ fontSize: 18 }} />
                </IconButton>
              </Tooltip>
              <Tooltip title="Inaccurate">
                <IconButton size="small" disabled={actionLoading} onClick={() => onComplete?.(session.id, 'inaccurate')} sx={{ color: 'error.main', p: 0.5 }}>
                  <ThumbDown sx={{ fontSize: 18 }} />
                </IconButton>
              </Tooltip>
            </>
          )}

          {group === 'in_progress' && (
            <>
              <Tooltip title="Accurate">
                <IconButton size="small" disabled={actionLoading} onClick={() => onComplete?.(session.id, 'accurate')} sx={{ color: 'success.main', p: 0.5 }}>
                  <ThumbUp sx={{ fontSize: 18 }} />
                </IconButton>
              </Tooltip>
              <Tooltip title="Partially Accurate">
                <IconButton size="small" disabled={actionLoading} onClick={() => onComplete?.(session.id, 'partially_accurate')} sx={{ color: 'warning.main', p: 0.5 }}>
                  <ThumbsUpDown sx={{ fontSize: 18 }} />
                </IconButton>
              </Tooltip>
              <Tooltip title="Inaccurate">
                <IconButton size="small" disabled={actionLoading} onClick={() => onComplete?.(session.id, 'inaccurate')} sx={{ color: 'error.main', p: 0.5 }}>
                  <ThumbDown sx={{ fontSize: 18 }} />
                </IconButton>
              </Tooltip>
              <Divider orientation="vertical" flexItem sx={{ mx: 0.25 }} />
              <Tooltip title="Unclaim">
                <IconButton
                  size="small"
                  disabled={actionLoading}
                  onClick={() => onUnclaim?.(session.id)}
                  sx={{ p: 0.5 }}
                >
                  <PersonRemove sx={{ fontSize: 16 }} />
                </IconButton>
              </Tooltip>
            </>
          )}

          {group === 'reviewed' && (
            <>
              <Tooltip title="Edit feedback">
                <IconButton
                  size="small"
                  onClick={() => onEditFeedback?.(
                    session.id,
                    session.quality_rating ?? '',
                    session.action_taken ?? '',
                    session.investigation_feedback ?? '',
                  )}
                  sx={{
                    p: 0.5,
                    color: (session.action_taken || session.investigation_feedback) ? 'primary.main' : 'text.disabled',
                  }}
                >
                  <EditNote sx={{ fontSize: 16 }} />
                </IconButton>
              </Tooltip>
              <Tooltip title="Reopen">
                <IconButton
                  size="small"
                  disabled={actionLoading}
                  onClick={() => onReopen?.(session.id)}
                  sx={{ p: 0.5 }}
                >
                  <Replay sx={{ fontSize: 16 }} />
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
