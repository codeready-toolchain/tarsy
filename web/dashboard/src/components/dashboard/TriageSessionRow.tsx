import {
  TableRow,
  TableCell,
  Typography,
  Button,
  Chip,
  Tooltip,
  IconButton,
  Box,
} from '@mui/material';
import {
  OpenInNew,
  Undo,
} from '@mui/icons-material';
import { useNavigate } from 'react-router-dom';
import { StatusBadge } from '../common/StatusBadge.tsx';
import { ScoreBadge } from '../common/ScoreBadge.tsx';
import { SummaryTooltip } from './SummaryTooltip.tsx';
import { formatTimestamp, compactTimeAgo } from '../../utils/format.ts';
import { sessionDetailPath, sessionScoringPath } from '../../constants/routes.ts';
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

const resolutionReasonConfig: Record<string, { label: string; color: 'success' | 'default' }> = {
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

  const hasActions = group !== 'investigating';

  return (
    <TableRow
      hover
      onClick={handleRowClick}
      sx={{
        cursor: 'pointer',
        '&:hover .triage-actions': { opacity: 1 },
      }}
    >
      {/* Status + Summary hover */}
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

      {/* Eval Score */}
      <TableCell
        onClick={(e) => {
          if (session.scoring_status || session.latest_score != null) {
            e.stopPropagation();
            navigate(sessionScoringPath(session.id));
          }
        }}
        sx={session.scoring_status || session.latest_score != null ? { cursor: 'pointer' } : undefined}
      >
        <ScoreBadge score={session.latest_score} scoringStatus={session.scoring_status} variant="pill" showLabel={false} />
      </TableCell>

      {/* Time */}
      <TableCell>
        <Tooltip title={formatTimestamp(session.created_at, 'absolute')}>
          <Typography variant="body2" color="text.secondary">
            {compactTimeAgo(session.created_at)}
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
                  label={resolutionReasonConfig[session.resolution_reason]?.label ?? session.resolution_reason}
                  color={resolutionReasonConfig[session.resolution_reason]?.color ?? 'default'}
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
      </TableCell>
    </TableRow>
  );
}
