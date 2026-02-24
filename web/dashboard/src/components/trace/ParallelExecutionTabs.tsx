/**
 * ParallelExecutionTabs — tabbed view for parallel agent executions.
 *
 * Aggregate summary box with status counts, duration, and token usage.
 * One tab per agent execution, each with metadata and interaction list.
 *
 * Visual pattern from old dashboard's ParallelStageExecutionTabs.tsx,
 * data layer rewritten for new TraceExecutionGroup and ExecutionOverview types.
 */

import { useState } from 'react';
import {
  Box,
  Typography,
  Tabs,
  Tab,
  Chip,
  Stack,
  Alert,
  alpha,
} from '@mui/material';

import type { TraceStageGroup } from '../../types/trace';
import type { SessionDetailResponse, ExecutionOverview } from '../../types/session';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import { formatDurationMs, formatTimestamp } from '../../utils/format';
import {
  findExecutionOverview,
  getStageStatusIcon,
  getStageStatusColor,
  getStageStatusDisplayName,
  getExecutionStatusCounts,
  getAggregateTotalTokens,
  getAggregateDuration,
  mergeAndSortInteractions,
} from './traceHelpers';
import InteractionCard from './InteractionCard';

interface ParallelExecutionTabsProps {
  stage: TraceStageGroup;
  session: SessionDetailResponse;
}

export default function ParallelExecutionTabs({ stage, session }: ParallelExecutionTabsProps) {
  const [activeTab, setActiveTab] = useState(0);

  // Gather execution overviews from session detail
  const executionOverviews = stage.executions
    .map((exec) => findExecutionOverview(session, exec.execution_id))
    .filter((e): e is ExecutionOverview => e != null);

  const statusCounts = getExecutionStatusCounts(executionOverviews);
  const aggregateTokens = getAggregateTotalTokens(executionOverviews);
  const aggregateDuration = getAggregateDuration(executionOverviews);

  const currentExecution = stage.executions[activeTab];
  const currentOverview = currentExecution
    ? findExecutionOverview(session, currentExecution.execution_id)
    : undefined;
  const interactions = currentExecution ? mergeAndSortInteractions(currentExecution) : [];

  return (
    <Box>
      {/* Aggregate Summary Box */}
      <Box
        sx={(theme) => ({
          p: 2,
          mb: 2,
          bgcolor: alpha(theme.palette.secondary.main, 0.04),
          border: 1,
          borderColor: alpha(theme.palette.secondary.main, 0.2),
          borderRadius: 2,
        })}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
          <Chip
            label="Parallel Execution"
            size="small"
            color="secondary"
            sx={{ fontWeight: 600 }}
          />
          <Typography variant="body2" color="text.secondary">
            {stage.executions.length} agents
          </Typography>
        </Box>

        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1.5, alignItems: 'center' }}>
          {/* Status counts */}
          {statusCounts.completed > 0 && (
            <Chip
              label={`${statusCounts.completed} completed`}
              size="small"
              color="success"
              variant="outlined"
              sx={{ fontWeight: 600, fontSize: '0.75rem' }}
            />
          )}
          {statusCounts.failed > 0 && (
            <Chip
              label={`${statusCounts.failed} failed`}
              size="small"
              color="error"
              variant="outlined"
              sx={{ fontWeight: 600, fontSize: '0.75rem' }}
            />
          )}
          {statusCounts.active > 0 && (
            <Chip
              label={`${statusCounts.active} running`}
              size="small"
              color="primary"
              variant="outlined"
              sx={{ fontWeight: 600, fontSize: '0.75rem' }}
            />
          )}
          {statusCounts.pending > 0 && (
            <Chip
              label={`${statusCounts.pending} pending`}
              size="small"
              variant="outlined"
              sx={{ fontWeight: 600, fontSize: '0.75rem' }}
            />
          )}
          {statusCounts.cancelled > 0 && (
            <Chip
              label={`${statusCounts.cancelled} cancelled`}
              size="small"
              color="warning"
              variant="outlined"
              sx={{ fontWeight: 600, fontSize: '0.75rem' }}
            />
          )}

          {/* Duration */}
          {aggregateDuration != null && (
            <Typography variant="body2" color="text.secondary" sx={{ fontWeight: 500 }}>
              Max duration: {formatDurationMs(aggregateDuration)}
            </Typography>
          )}

          {/* Tokens */}
          {aggregateTokens.total_tokens > 0 && (
            <TokenUsageDisplay
              tokenData={aggregateTokens}
              variant="inline"
              size="small"
            />
          )}
        </Box>
      </Box>

      {/* Agent tabs */}
      <Box sx={{ borderBottom: 1, borderColor: 'divider' }}>
        <Tabs
          value={activeTab}
          onChange={(_, newValue) => setActiveTab(newValue)}
          variant="scrollable"
          scrollButtons="auto"
          aria-label="Agent execution tabs"
        >
          {stage.executions.map((exec, idx) => {
            const overview = findExecutionOverview(session, exec.execution_id);
            const statusColor = overview ? getStageStatusColor(overview.status) : 'default';
            const statusIcon = overview ? getStageStatusIcon(overview.status) : undefined;
            return (
              <Tab
                key={exec.execution_id}
                label={
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                    {statusIcon}
                    <Typography variant="body2" sx={{ fontWeight: 600, textTransform: 'none' }}>
                      {exec.agent_name}
                    </Typography>
                    <Chip
                      label={overview?.status ?? 'unknown'}
                      size="small"
                      color={statusColor === 'default' ? undefined : statusColor}
                      variant="outlined"
                      sx={{ fontSize: '0.65rem', height: 18 }}
                    />
                  </Box>
                }
                id={`execution-tab-${idx}`}
                aria-controls={`execution-tabpanel-${idx}`}
              />
            );
          })}
        </Tabs>
      </Box>

      {/* Tab panel */}
      <Box
        role="tabpanel"
        id={`execution-tabpanel-${activeTab}`}
        aria-labelledby={`execution-tab-${activeTab}`}
        sx={{ pt: 2 }}
      >
        {/* Execution metadata */}
        {currentOverview && (
          <Box
            sx={{
              p: 2,
              mb: 2,
              bgcolor: 'grey.50',
              border: 1,
              borderColor: 'divider',
              borderRadius: 1,
            }}
          >
            <Stack spacing={1}>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 2, alignItems: 'center' }}>
                <Typography variant="body2" color="text.secondary">
                  <strong>Agent:</strong> {currentOverview.agent_name}
                </Typography>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                  {getStageStatusIcon(currentOverview.status)}
                  <Chip
                    label={getStageStatusDisplayName(currentOverview.status)}
                    size="small"
                    color={getStageStatusColor(currentOverview.status) === 'default'
                      ? undefined
                      : getStageStatusColor(currentOverview.status)}
                    sx={{ fontWeight: 600, fontSize: '0.75rem', height: 22 }}
                  />
                </Box>
                {currentOverview.llm_backend && (
                  <Typography variant="body2" color="text.secondary">
                    <strong>Backend:</strong> {currentOverview.llm_backend}
                  </Typography>
                )}
                {currentOverview.llm_provider && (
                  <Typography variant="body2" color="text.secondary">
                    <strong>Provider:</strong> {currentOverview.llm_provider}
                  </Typography>
                )}
              </Box>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 2 }}>
                {currentOverview.started_at && (
                  <Typography variant="body2" color="text.secondary">
                    <strong>Started:</strong> {formatTimestamp(currentOverview.started_at, 'short')}
                  </Typography>
                )}
                {currentOverview.completed_at && (
                  <Typography variant="body2" color="text.secondary">
                    <strong>Completed:</strong> {formatTimestamp(currentOverview.completed_at, 'short')}
                  </Typography>
                )}
                {currentOverview.duration_ms != null && (
                  <Typography variant="body2" color="text.secondary">
                    <strong>Duration:</strong> {formatDurationMs(currentOverview.duration_ms)}
                  </Typography>
                )}
              </Box>

              {currentOverview.total_tokens > 0 && (
                <TokenUsageDisplay
                  tokenData={{
                    input_tokens: currentOverview.input_tokens,
                    output_tokens: currentOverview.output_tokens,
                    total_tokens: currentOverview.total_tokens,
                  }}
                  variant="compact"
                  size="small"
                  showBreakdown
                  label="Tokens"
                  color="info"
                />
              )}
            </Stack>
          </Box>
        )}

        {/* Error alert */}
        {currentOverview?.error_message && (
          <Alert severity="error" sx={{ mb: 2 }}>
            <Typography variant="body2">{currentOverview.error_message}</Typography>
          </Alert>
        )}

        {/* Interaction list */}
        {interactions.length > 0 ? (
          <Stack spacing={2}>
            {interactions.map((interaction) => (
              <InteractionCard
                key={interaction.id}
                interaction={interaction}
                sessionId={session.id}
              />
            ))}
          </Stack>
        ) : (
          <Typography variant="body2" color="text.secondary" sx={{ py: 2, textAlign: 'center' }}>
            No interactions recorded for this execution
          </Typography>
        )}
      </Box>
    </Box>
  );
}
