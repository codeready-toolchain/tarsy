/**
 * PostInvestigationCard — outcome summary of the session's post-investigation
 * stages: one row per entry with status, acted / no-action chip and memo.
 *
 * The memo is the stage's top-level final_analysis timeline event; sub-agent
 * final answers are skipped. The full trace stays in the conversation timeline.
 *
 * A finished all-no-action card starts folded, with the outcome chip on the
 * header. A running card stays open and shows the same info spinner the
 * final-analysis card uses for scoring. A manual toggle is kept across refetch.
 */

import { useMemo, useRef, useState } from 'react';
import type { AnchorHTMLAttributes } from 'react';
import { Paper, Typography, Box, Chip, IconButton, Collapse, Divider, CircularProgress, Tooltip } from '@mui/material';
import type { ChipProps } from '@mui/material';
import { ExpandLess, UnfoldMore, AssignmentTurnedInOutlined } from '@mui/icons-material';
import { alpha } from '@mui/material/styles';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';

import { remarkPlugins, finalAnswerMarkdownComponents } from '../../utils/markdownComponents.tsx';
import { STAGE_TYPE, TIMELINE_EVENT_TYPES } from '../../constants/eventTypes.ts';
import { EXECUTION_STATUS, TERMINAL_EXECUTION_STATUSES } from '../../constants/sessionStatus.ts';
import type { StageOverview, TimelineEvent } from '../../types/session.ts';

interface PostInvestigationCardProps {
  stages: StageOverview[];
  timelineEvents: TimelineEvent[];
  /** Increment to collapse from outside. */
  collapseCounter?: number;
}

const STATUS_CHIP: Record<string, { label: string; color: ChipProps['color'] }> = {
  [EXECUTION_STATUS.PENDING]: { label: 'Pending', color: 'default' },
  [EXECUTION_STATUS.STARTED]: { label: 'Running', color: 'info' },
  [EXECUTION_STATUS.ACTIVE]: { label: 'Running', color: 'info' },
  [EXECUTION_STATUS.COMPLETED]: { label: 'Completed', color: 'success' },
  [EXECUTION_STATUS.FAILED]: { label: 'Failed', color: 'error' },
  [EXECUTION_STATUS.TIMED_OUT]: { label: 'Timed out', color: 'error' },
  [EXECUTION_STATUS.CANCELLED]: { label: 'Cancelled', color: 'default' },
};

// Memos link to artifacts the agent created (issues, tickets); open them in a new tab.
const memoMarkdownComponents = {
  ...finalAnswerMarkdownComponents,
  a: ({ node: _node, children, ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { node?: unknown }) => (
    <a {...props} target="_blank" rel="noopener noreferrer">
      {children}
    </a>
  ),
};

function postInvestigationEntries(stages: StageOverview[]): StageOverview[] {
  return stages
    .filter((s) => s.stage_type === STAGE_TYPE.POST_INVESTIGATION)
    .sort((a, b) => a.stage_index - b.stage_index);
}

/** Every entry finished and explicitly took no action. A missing YES/NO marker stays open. */
function allNoAction(entries: StageOverview[]): boolean {
  return entries.length > 0 && entries.every(
    (s) => TERMINAL_EXECUTION_STATUSES.has(s.status) && s.actions_executed === false,
  );
}

function anyRunning(entries: StageOverview[]): boolean {
  return entries.some((s) => !TERMINAL_EXECUTION_STATUSES.has(s.status));
}

/** Latest top-level final_analysis content per post-investigation stage. */
function memosByStage(timelineEvents: TimelineEvent[]): Map<string, string> {
  const latest = new Map<string, TimelineEvent>();
  for (const ev of timelineEvents) {
    if (ev.event_type !== TIMELINE_EVENT_TYPES.FINAL_ANALYSIS || !ev.stage_id || ev.parent_execution_id) {
      continue;
    }
    const prev = latest.get(ev.stage_id);
    if (!prev || ev.sequence_number > prev.sequence_number) {
      latest.set(ev.stage_id, ev);
    }
  }
  return new Map([...latest].map(([stageId, ev]) => [stageId, ev.content]));
}

export default function PostInvestigationCard({
  stages,
  timelineEvents,
  collapseCounter = 0,
}: PostInvestigationCardProps) {
  const entries = useMemo(() => postInvestigationEntries(stages), [stages]);
  const quiet = allNoAction(entries);
  const running = anyRunning(entries);
  // Start folded only when the loaded data is already a no-action outcome.
  const [isExpanded, setIsExpanded] = useState(() => !allNoAction(postInvestigationEntries(stages)));
  // A manual toggle wins over a later refetch that is still no-action.
  const userToggled = useRef(false);

  const [prevCollapseCounter, setPrevCollapseCounter] = useState(collapseCounter);
  if (collapseCounter !== prevCollapseCounter) {
    setPrevCollapseCounter(collapseCounter);
    if (collapseCounter > 0) setIsExpanded(false);
  }

  const [seenQuiet, setSeenQuiet] = useState(quiet);
  if (quiet !== seenQuiet) {
    setSeenQuiet(quiet);
    if (quiet && !userToggled.current) setIsExpanded(false);
  }

  const memos = useMemo(() => memosByStage(timelineEvents), [timelineEvents]);
  const toggleExpanded = () => {
    userToggled.current = true;
    setIsExpanded((open) => !open);
  };

  if (entries.length === 0) {
    return null;
  }

  return (
    <Paper sx={{ p: 2.5 }} data-testid="post-investigation-card">
      <Box
        onClick={toggleExpanded}
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
              bgcolor: (theme) => alpha(theme.palette.success.main, 0.15),
              border: '2px solid',
              borderColor: 'success.main',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              flexShrink: 0,
            }}
          >
            <AssignmentTurnedInOutlined sx={{ fontSize: 24, color: 'success.main' }} />
          </Box>
          <Typography variant="h6">Post-investigation</Typography>
          <Chip
            label={entries.length}
            size="small"
            variant="outlined"
            sx={{ ml: 0.5, height: 20, fontSize: '0.75rem' }}
          />
          {running && <RunningPill />}
          {!isExpanded && quiet && (
            <Chip label="No action" color="default" size="small" data-testid="post-investigation-header-outcome" />
          )}
        </Box>
        <IconButton
          size="small"
          onClick={(e) => { e.stopPropagation(); toggleExpanded(); }}
          aria-label={isExpanded ? 'Collapse post-investigation' : 'Expand post-investigation'}
          sx={{
            bgcolor: (theme) => alpha(theme.palette.primary.main, 0.12),
            '&:hover': { bgcolor: (theme) => alpha(theme.palette.primary.main, 0.22) },
          }}
        >
          {isExpanded ? <ExpandLess /> : <UnfoldMore />}
        </IconButton>
      </Box>

      <Collapse in={isExpanded} timeout={400}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {entries.map((stage, idx) => (
            <Box key={stage.id}>
              {idx > 0 && <Divider sx={{ mb: 2 }} />}
              <EntryRow stage={stage} memo={memos.get(stage.id)} />
            </Box>
          ))}
        </Box>
      </Collapse>
    </Paper>
  );
}

/** Same info pill as the eval-score spinner on the final-analysis card. */
function RunningPill() {
  return (
    <Tooltip title="Post-investigation in progress">
      <Box
        sx={(theme) => ({
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
          height: 32,
          px: 1,
          boxSizing: 'border-box',
          backgroundColor: alpha(theme.palette.info.main, 0.08),
          borderRadius: '16px',
          border: '1px solid',
          borderColor: alpha(theme.palette.info.main, 0.25),
          color: 'info.main',
        })}
      >
        <CircularProgress size={16} color="inherit" aria-label="Post-investigation in progress" />
      </Box>
    </Tooltip>
  );
}

function EntryRow({ stage, memo }: { stage: StageOverview; memo?: string }) {
  const status = STATUS_CHIP[stage.status] ?? { label: stage.status, color: 'default' as const };
  const agentName = stage.executions?.find((e) => !e.parent_execution_id)?.agent_name;
  const terminal = TERMINAL_EXECUTION_STATUSES.has(stage.status);

  return (
    <Box data-testid={`post-investigation-entry-${stage.stage_name}`}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', mb: 1 }}>
        <Typography variant="subtitle1" sx={{ fontWeight: 600 }}>
          {stage.stage_name}
        </Typography>
        {agentName && (
          <Typography variant="body2" sx={{ color: 'text.secondary' }}>
            {agentName}
          </Typography>
        )}
        <Chip label={status.label} color={status.color} size="small" variant="outlined" />
        {stage.actions_executed != null && (
          <Chip
            label={stage.actions_executed ? 'Acted' : 'No action'}
            color={stage.actions_executed ? 'success' : 'default'}
            size="small"
          />
        )}
      </Box>
      {memo ? (
        <Box sx={{ wordBreak: 'break-word' }}>
          <ReactMarkdown
            remarkPlugins={remarkPlugins}
            urlTransform={defaultUrlTransform}
            components={memoMarkdownComponents}
          >
            {memo}
          </ReactMarkdown>
        </Box>
      ) : (
        <Typography variant="body2" sx={{ color: 'text.secondary', fontStyle: 'italic' }}>
          {terminal ? 'No memo' : 'Running…'}
        </Typography>
      )}
    </Box>
  );
}
