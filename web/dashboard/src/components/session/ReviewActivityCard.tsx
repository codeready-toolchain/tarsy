import { useState, useEffect } from 'react';
import {
  Paper,
  Typography,
  Box,
  Chip,
  IconButton,
  Collapse,
  Skeleton,
  Alert,
  Button,
  Tooltip,
} from '@mui/material';
import {
  ExpandLess,
  UnfoldMore,
  History,
  PersonAdd,
  PersonRemove,
  CheckCircle,
  Replay,
  Edit,
  DoneAll,
} from '@mui/icons-material';
import { alpha } from '@mui/material/styles';
import type { SvgIconComponent } from '@mui/icons-material';

import { getReviewActivity } from '../../services/api.ts';
import { getRatingConfig } from '../../constants/ratingConfig.ts';
import { timeAgo, formatTimestamp } from '../../utils/format.ts';
import type { ReviewActivityItem } from '../../types/api.ts';

interface ReviewActivityCardProps {
  sessionId: string;
  /** Increment to trigger a refetch (e.g. after WebSocket review.status event). */
  refreshCounter?: number;
  /** Increment to collapse from outside. */
  collapseCounter?: number;
}

interface ActionConfig {
  icon: SvgIconComponent;
  color: 'info' | 'warning' | 'success' | 'error' | 'default';
  label: string;
}

const ACTION_CONFIG: Record<string, ActionConfig> = {
  claim: { icon: PersonAdd, color: 'info', label: 'Claimed' },
  unclaim: { icon: PersonRemove, color: 'default', label: 'Unclaimed' },
  complete: { icon: CheckCircle, color: 'success', label: 'Completed Review' },
  reopen: { icon: Replay, color: 'warning', label: 'Reopened' },
  update_feedback: { icon: Edit, color: 'default', label: 'Updated Feedback' },
  acknowledge: { icon: DoneAll, color: 'default', label: 'Acknowledged' },
};

function getActionConfig(action: string): ActionConfig {
  return ACTION_CONFIG[action] ?? { icon: History, color: 'default', label: action };
}

function getIconColor(item: ReviewActivityItem): string {
  if (item.action === 'complete' || item.action === 'update_feedback') {
    const ratingCfg = getRatingConfig(item.quality_rating);
    if (ratingCfg) {
      return `${ratingCfg.color}.main`;
    }
  }
  const cfg = getActionConfig(item.action);
  if (cfg.color === 'default') return 'text.secondary';
  return `${cfg.color}.main`;
}

function hasDetails(item: ReviewActivityItem): boolean {
  if (item.action === 'acknowledge') return false;
  return !!(item.quality_rating || item.note || item.investigation_feedback);
}

export default function ReviewActivityCard({
  sessionId,
  refreshCounter = 0,
  collapseCounter = 0,
}: ReviewActivityCardProps) {
  const [activities, setActivities] = useState<ReviewActivityItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<Error | null>(null);
  const [isExpanded, setIsExpanded] = useState(false);
  const [retryCount, setRetryCount] = useState(0);

  const [prevCollapseCounter, setPrevCollapseCounter] = useState(collapseCounter);
  if (collapseCounter !== prevCollapseCounter) {
    setPrevCollapseCounter(collapseCounter);
    if (collapseCounter > 0) setIsExpanded(false);
  }

  const [prevFetchKey, setPrevFetchKey] = useState(`${sessionId}:${refreshCounter}`);
  const fetchKey = `${sessionId}:${refreshCounter}`;
  if (fetchKey !== prevFetchKey) {
    setPrevFetchKey(fetchKey);
    setLoading(true);
    setFetchError(null);
  }

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const data = await getReviewActivity(sessionId);
        if (!cancelled) setActivities(data.activities);
      } catch (err) {
        if (!cancelled) setFetchError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [sessionId, refreshCounter, retryCount]);

  if (loading) {
    return <Skeleton variant="rectangular" height={60} sx={{ borderRadius: 1 }} />;
  }

  if (fetchError) {
    return (
      <Alert
        severity="warning"
        action={
          <Button
            color="inherit"
            size="small"
            onClick={() => {
              setFetchError(null);
              setLoading(true);
              setRetryCount((c) => c + 1);
            }}
          >
            Retry
          </Button>
        }
      >
        Failed to load review activity.
      </Alert>
    );
  }

  if (activities.length === 0) {
    return null;
  }

  return (
    <Paper sx={{ p: 2.5 }}>
      <Box
        onClick={() => setIsExpanded(!isExpanded)}
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          mb: isExpanded ? 2 : 0,
          cursor: 'pointer',
          '&:hover': { opacity: 0.8 },
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
          <Box
            sx={{
              width: 40,
              height: 40,
              borderRadius: '50%',
              bgcolor: (theme) => alpha(theme.palette.info.main, 0.15),
              border: '2px solid',
              borderColor: 'info.main',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              flexShrink: 0,
            }}
          >
            <History sx={{ fontSize: 24, color: 'info.main' }} />
          </Box>
          <Typography variant="h6">Review Activity</Typography>
          <Chip
            label={activities.length}
            size="small"
            variant="outlined"
            sx={{ ml: 0.5, height: 20, fontSize: '0.75rem' }}
          />
        </Box>
        <IconButton
          size="small"
          onClick={(e) => { e.stopPropagation(); setIsExpanded(!isExpanded); }}
          aria-label={isExpanded ? 'Collapse review activity' : 'Expand review activity'}
          sx={{
            bgcolor: (theme) => alpha(theme.palette.primary.main, 0.12),
            '&:hover': { bgcolor: (theme) => alpha(theme.palette.primary.main, 0.22) },
          }}
        >
          {isExpanded ? <ExpandLess /> : <UnfoldMore />}
        </IconButton>
      </Box>

      <Collapse in={isExpanded} timeout={400}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
          {activities.map((item, idx) => (
            <ActivityRow key={item.id} item={item} isLast={idx === activities.length - 1} />
          ))}
        </Box>
      </Collapse>
    </Paper>
  );
}

function ActivityRow({ item, isLast }: { item: ReviewActivityItem; isLast: boolean }) {
  const cfg = getActionConfig(item.action);
  const IconComponent = cfg.icon;
  const iconColor = getIconColor(item);
  const showDetails = hasDetails(item);
  const ratingCfg = getRatingConfig(item.quality_rating);

  return (
    <Box sx={{ display: 'flex', gap: 1.5, position: 'relative' }}>
      {/* Vertical connector line */}
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          width: 32,
          flexShrink: 0,
        }}
      >
        <Box
          sx={{
            width: 28,
            height: 28,
            borderRadius: '50%',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            bgcolor: (theme) => alpha(theme.palette.action.hover, 0.08),
          }}
        >
          <IconComponent sx={{ fontSize: 16, color: iconColor }} />
        </Box>
        {!isLast && (
          <Box
            sx={{
              flex: 1,
              width: 2,
              bgcolor: 'divider',
              minHeight: 16,
            }}
          />
        )}
      </Box>

      {/* Content */}
      <Box sx={{ flex: 1, pb: isLast ? 0 : 1.5, minWidth: 0 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
          <Typography variant="body2" sx={{ fontWeight: 600 }}>
            {cfg.label}
          </Typography>
          <Typography variant="body2" color="text.secondary">
            by {item.actor}
          </Typography>
          <Tooltip title={formatTimestamp(item.created_at, 'absolute')} arrow>
            <Typography variant="caption" color="text.disabled" sx={{ ml: 'auto' }}>
              {timeAgo(item.created_at)}
            </Typography>
          </Tooltip>
        </Box>

        {showDetails && (
          <Box sx={{ mt: 0.75, display: 'flex', flexDirection: 'column', gap: 0.5 }}>
            {ratingCfg && (
              <Chip
                label={ratingCfg.label}
                size="small"
                color={ratingCfg.color}
                variant="outlined"
                sx={{ alignSelf: 'flex-start', height: 22, fontSize: '0.7rem' }}
              />
            )}
            {item.note && (
              <Typography variant="body2" color="text.secondary" sx={{ whiteSpace: 'pre-wrap' }}>
                {item.note}
              </Typography>
            )}
            {item.investigation_feedback && (
              <Typography
                variant="body2"
                color="text.secondary"
                sx={{ whiteSpace: 'pre-wrap', fontStyle: 'italic' }}
              >
                {item.investigation_feedback}
              </Typography>
            )}
          </Box>
        )}
      </Box>
    </Box>
  );
}
