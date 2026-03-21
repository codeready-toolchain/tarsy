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
import { PersonRemove, Replay } from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { SummaryTooltip } from './SummaryTooltip.tsx';
import { ScoreCell } from './ScoreCell.tsx';
import { ReviewCell } from './ReviewCell.tsx';
import { qualityEvalScoreBodySx } from './qualityGroupSx.ts';
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
  onReopen?: (sessionId: string) => void;
  onReviewClick?: (session: DashboardSessionItem) => void;
  actionLoading?: boolean;
}

export function TriageSessionRow({
  session,
  group,
  selected,
  selectionDisabled,
  onToggleSelect,
  onClaim,
  onUnclaim,
  onReopen,
  onReviewClick,
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
        '&:hover': {
          '& .triage-actions': { opacity: 1 },
          '& .review-hover-icon': { opacity: 1 },
        },
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

      {/* Time */}
      <TableCell>
        <Tooltip title={formatTimestamp(session.created_at, 'absolute')}>
          <Typography variant="body2" color="text.secondary">
            {formatTimestamp(session.created_at, 'short')}
          </Typography>
        </Tooltip>
      </TableCell>

      {/* Eval Score */}
      <ScoreCell
        sessionId={session.id}
        score={session.latest_score}
        scoringStatus={session.scoring_status}
        sx={qualityEvalScoreBodySx}
      />

      {/* Review */}
      <ReviewCell session={session} onReviewClick={onReviewClick} />

      {/* Actions — only Claim/Unclaim/Reopen + open-in-tab; reviewing is via Review column */}
      <TableCell sx={{ width: 100, textAlign: 'right' }}>
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
            <Button
              size="small"
              variant="outlined"
              disabled={actionLoading}
              onClick={() => onClaim?.(session.id)}
              sx={{ textTransform: 'none', fontSize: '0.7rem', py: 0.125, px: 1, minWidth: 'auto', lineHeight: 1.5 }}
            >
              Claim
            </Button>
          )}

          {group === 'in_progress' && (
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
          )}

          {group === 'reviewed' && (
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
          )}

          <OpenNewTabButton sessionId={session.id} />
        </Box>
      </TableCell>
    </TableRow>
  );
}
