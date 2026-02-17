/**
 * TraceTimeline — main trace component with chain header, breadcrumbs,
 * progress bar, stage accordion list, and session-level interactions.
 *
 * Visual pattern from old dashboard's TechnicalTimeline.tsx / NestedAccordionTimeline.tsx,
 * data layer rewritten for new TraceListResponse and SessionDetailResponse.
 */

import { useState, useMemo, useCallback } from 'react';
import {
  Box,
  Typography,
  Paper,
  Chip,
  IconButton,
  LinearProgress,
  Breadcrumbs,
  Link,
  Stack,
  Divider,
  alpha,
} from '@mui/material';
import {
  NavigateBefore,
  NavigateNext,
  AccountTree,
} from '@mui/icons-material';

import type { TraceListResponse } from '../../types/trace';
import type { SessionDetailResponse } from '../../types/session';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';
import CopyButton from '../shared/CopyButton';
import {
  findStageOverview,
  countStageInteractions,
  formatEntireFlowForCopy,
  getStageStatusColor,
} from './traceHelpers';
import StageAccordion from './StageAccordion';
import InteractionCard from './InteractionCard';
import type { LLMInteractionListItem } from '../../types/trace';
import type { UnifiedInteraction } from './traceHelpers';

interface TraceTimelineProps {
  traceData: TraceListResponse;
  session: SessionDetailResponse;
}

export default function TraceTimeline({ traceData, session }: TraceTimelineProps) {
  const [currentStageIndex, setCurrentStageIndex] = useState(0);

  const totalStages = traceData.stages.length;

  // Progress stats
  const { completedStages, failedStages, progressPercent } = useMemo(() => {
    let completed = 0;
    let failed = 0;
    for (const stage of traceData.stages) {
      const overview = findStageOverview(session, stage.stage_id);
      if (overview?.status === EXECUTION_STATUS.COMPLETED) completed++;
      if (
        overview?.status === EXECUTION_STATUS.FAILED ||
        overview?.status === EXECUTION_STATUS.TIMED_OUT
      )
        failed++;
    }
    const progress = totalStages > 0 ? ((completed + failed) / totalStages) * 100 : 0;
    return { completedStages: completed, failedStages: failed, progressPercent: progress };
  }, [traceData.stages, session, totalStages]);

  // Total interactions
  const totalInteractions = useMemo(() => {
    let count = 0;
    for (const stage of traceData.stages) {
      count += countStageInteractions(stage).total;
    }
    count += traceData.session_interactions.length;
    return count;
  }, [traceData]);

  // Copy entire flow
  const entireFlowText = useMemo(
    () => formatEntireFlowForCopy(traceData, session),
    [traceData, session],
  );

  // Stage navigation
  const handlePrevStage = useCallback(() => {
    setCurrentStageIndex((prev) => Math.max(0, prev - 1));
  }, []);

  const handleNextStage = useCallback(() => {
    setCurrentStageIndex((prev) => Math.min(totalStages - 1, prev + 1));
  }, [totalStages]);

  const handleBreadcrumbClick = useCallback((index: number) => {
    setCurrentStageIndex(index);
  }, []);

  // Session-level interactions as unified interactions
  const sessionInteractions = useMemo<UnifiedInteraction[]>(
    () =>
      traceData.session_interactions.map((i: LLMInteractionListItem) => ({
        id: i.id,
        kind: 'llm' as const,
        interaction_type: i.interaction_type,
        created_at: i.created_at,
        duration_ms: i.duration_ms,
        error_message: i.error_message,
        model_name: i.model_name,
        input_tokens: i.input_tokens,
        output_tokens: i.output_tokens,
        total_tokens: i.total_tokens,
      })),
    [traceData.session_interactions],
  );

  if (totalStages === 0 && sessionInteractions.length === 0) {
    return (
      <Paper sx={{ p: 4, textAlign: 'center' }}>
        <AccountTree sx={{ fontSize: 48, color: 'text.disabled', mb: 2 }} />
        <Typography variant="h6" color="text.secondary" gutterBottom>
          No trace data available
        </Typography>
        <Typography variant="body2" color="text.secondary">
          Trace data will appear here once the session starts processing.
        </Typography>
      </Paper>
    );
  }

  return (
    <Box>
      {/* Chain Progress Header */}
      <Paper
        elevation={2}
        sx={{
          p: 2,
          mb: 3,
          borderRadius: 2,
        }}
      >
        {/* Top row: chain ID + copy + navigation */}
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
            <AccountTree color="primary" />
            <Typography variant="h6" sx={{ fontWeight: 600 }}>
              Chain: {session.chain_id}
            </Typography>
          </Box>

          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <CopyButton
              text={entireFlowText}
              size="small"
              label="Copy Entire Flow"
              tooltip="Copy the entire execution trace to clipboard"
            />
          </Box>
        </Box>

        {/* Stage navigation */}
        {totalStages > 0 && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
            <IconButton
              size="small"
              onClick={handlePrevStage}
              disabled={currentStageIndex === 0}
              aria-label="Previous stage"
            >
              <NavigateBefore />
            </IconButton>

            <Chip
              label={`Stage ${currentStageIndex + 1} of ${totalStages}`}
              size="small"
              color="primary"
              variant="outlined"
              sx={{ fontWeight: 600 }}
            />

            <IconButton
              size="small"
              onClick={handleNextStage}
              disabled={currentStageIndex >= totalStages - 1}
              aria-label="Next stage"
            >
              <NavigateNext />
            </IconButton>

            <Box sx={{ flex: 1 }} />

            {/* Status chips */}
            <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
              <Chip
                label={`${totalStages} stages`}
                size="small"
                variant="outlined"
                sx={{ fontSize: '0.75rem', fontWeight: 600 }}
              />
              {completedStages > 0 && (
                <Chip
                  label={`${completedStages} completed`}
                  size="small"
                  color="success"
                  variant="outlined"
                  sx={{ fontSize: '0.75rem', fontWeight: 600 }}
                />
              )}
              {failedStages > 0 && (
                <Chip
                  label={`${failedStages} failed`}
                  size="small"
                  color="error"
                  variant="outlined"
                  sx={{ fontSize: '0.75rem', fontWeight: 600 }}
                />
              )}
              <Chip
                label={`${totalInteractions} interactions`}
                size="small"
                color="info"
                variant="outlined"
                sx={{ fontSize: '0.75rem', fontWeight: 600 }}
              />
            </Box>
          </Box>
        )}

        {/* Progress bar */}
        {totalStages > 0 && (
          <LinearProgress
            variant="determinate"
            value={progressPercent}
            sx={{
              height: 6,
              borderRadius: 3,
              bgcolor: 'grey.200',
              mb: 2,
              '& .MuiLinearProgress-bar': {
                borderRadius: 3,
              },
            }}
          />
        )}

        {/* Breadcrumbs */}
        {totalStages > 1 && (
          <Breadcrumbs
            maxItems={8}
            itemsAfterCollapse={3}
            itemsBeforeCollapse={3}
            separator=">"
            aria-label="Stage navigation"
          >
            {traceData.stages.map((stage, idx) => {
              const overview = findStageOverview(session, stage.stage_id);
              const color = getStageStatusColor(overview?.status ?? 'pending');
              const isCurrentNav = idx === currentStageIndex;

              return (
                <Link
                  key={stage.stage_id}
                  component="button"
                  variant="body2"
                  onClick={() => handleBreadcrumbClick(idx)}
                  underline={isCurrentNav ? 'always' : 'hover'}
                  sx={(theme) => ({
                    fontWeight: isCurrentNav ? 700 : 400,
                    color:
                      color === 'default'
                        ? theme.palette.text.secondary
                        : theme.palette[color].main,
                    bgcolor: isCurrentNav
                      ? alpha(
                          color === 'default'
                            ? theme.palette.grey[400]
                            : theme.palette[color].main,
                          0.1,
                        )
                      : 'transparent',
                    px: isCurrentNav ? 1 : 0.5,
                    py: 0.25,
                    borderRadius: 1,
                    cursor: 'pointer',
                    transition: 'all 0.15s ease-in-out',
                    '&:hover': {
                      bgcolor: alpha(
                        color === 'default'
                          ? theme.palette.grey[400]
                          : theme.palette[color].main,
                        0.08,
                      ),
                    },
                  })}
                >
                  {stage.stage_name}
                </Link>
              );
            })}
          </Breadcrumbs>
        )}
      </Paper>

      {/* Stage Accordions */}
      {traceData.stages.map((stage, idx) => (
        <StageAccordion
          key={stage.stage_id}
          stage={stage}
          stageIndex={idx}
          session={session}
          defaultExpanded={idx === currentStageIndex}
        />
      ))}

      {/* Session-Level Interactions */}
      {sessionInteractions.length > 0 && (
        <Box sx={{ mt: 3 }}>
          <Divider sx={{ mb: 2 }} />
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
            <Typography variant="h6" sx={{ fontWeight: 600 }}>
              Session-Level Interactions
            </Typography>
            <Chip
              label={`${sessionInteractions.length}`}
              size="small"
              color="info"
              sx={{ fontWeight: 600 }}
            />
          </Box>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
            These interactions run at the session level, outside the stage pipeline
            (e.g., executive summary generation).
          </Typography>
          <Stack spacing={2}>
            {sessionInteractions.map((interaction) => (
              <InteractionCard
                key={interaction.id}
                interaction={interaction}
                sessionId={session.id}
              />
            ))}
          </Stack>
        </Box>
      )}
    </Box>
  );
}
