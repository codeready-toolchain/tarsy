/**
 * StageAccordion — single stage accordion with metadata + interactions.
 *
 * Shows stage name, status, agent names, interaction count badges, duration,
 * and renders either a single agent's interaction list or ParallelExecutionTabs.
 *
 * Visual pattern from old dashboard's NestedAccordionTimeline.tsx accordion items,
 * data layer rewritten for new TraceStageGroup and StageOverview types.
 */

import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Avatar,
  Box,
  Typography,
  Chip,
  Stack,
  Alert,
} from '@mui/material';
import { alpha, useTheme } from '@mui/material/styles';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import { CallSplit } from '@mui/icons-material';

import type { TraceStageGroup } from '../../types/trace';
import type { SessionDetailResponse, ExecutionOverview } from '../../types/session';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import { formatDurationMs, formatTimestamp } from '../../utils/format';
import {
  findStageOverview,
  findExecutionOverview,
  computeStageDuration,
  countStageInteractions,
  getStageStatusIcon,
  getStageStatusColor,
  getStageStatusDisplayName,
  isParallelStage,
  mergeAndSortInteractions,
} from './traceHelpers';
import ParallelExecutionTabs from './ParallelExecutionTabs';
import InteractionCard from './InteractionCard';

interface StageAccordionProps {
  stage: TraceStageGroup;
  stageIndex: number;
  session: SessionDetailResponse;
  defaultExpanded?: boolean;
}

export default function StageAccordion({
  stage,
  stageIndex,
  session,
  defaultExpanded = false,
}: StageAccordionProps) {
  const theme = useTheme();
  const stageOverview = findStageOverview(session, stage.stage_id);
  const isParallel = isParallelStage(stage, stageOverview);
  const counts = countStageInteractions(stage);
  const duration = computeStageDuration(stageOverview);
  const status = stageOverview?.status ?? 'unknown';
  const statusColor = getStageStatusColor(status);

  // Single-agent execution info
  const singleExecution = !isParallel && stage.executions.length === 1 ? stage.executions[0] : null;
  const singleOverview: ExecutionOverview | undefined = singleExecution
    ? findExecutionOverview(session, singleExecution.execution_id)
    : undefined;
  const singleInteractions = singleExecution ? mergeAndSortInteractions(singleExecution) : [];

  // Agent names for summary
  const agentNames = stage.executions.map((e) => e.agent_name);

  return (
    <Accordion
      defaultExpanded={defaultExpanded}
      sx={{
        border: 1,
        borderColor: alpha(
          statusColor === 'default' ? theme.palette.grey[400] : theme.palette[statusColor].main,
          0.3,
        ),
        borderRadius: '8px !important',
        '&:before': { display: 'none' },
        mb: 2,
        overflow: 'hidden',
      }}
    >
      <AccordionSummary
        expandIcon={<ExpandMoreIcon />}
        sx={{
          bgcolor: alpha(
            statusColor === 'default' ? theme.palette.grey[400] : theme.palette[statusColor].main,
            0.04,
          ),
          '&:hover': {
            bgcolor: alpha(
              statusColor === 'default' ? theme.palette.grey[400] : theme.palette[statusColor].main,
              0.08,
            ),
          },
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5, flex: 1, flexWrap: 'wrap', pr: 2 }}>
          {/* Status icon avatar */}
          <Avatar
            sx={{
              width: 32,
              height: 32,
              bgcolor: statusColor === 'default' ? 'grey.400' : `${statusColor}.main`,
              color: 'white',
            }}
          >
            {getStageStatusIcon(status)}
          </Avatar>

          {/* Stage number and name */}
          <Typography variant="subtitle1" sx={{ fontWeight: 600 }}>
            Stage {stageIndex + 1}: {stage.stage_name}
          </Typography>

          {/* Parallel badge */}
          {isParallel && (
            <Chip
              icon={<CallSplit sx={{ fontSize: '1rem' }} />}
              label={`${stage.executions.length} agents`}
              size="small"
              color="secondary"
              variant="outlined"
              sx={{ fontWeight: 600, fontSize: '0.7rem' }}
            />
          )}

          {/* Agent names (single agent) */}
          {!isParallel && agentNames.length === 1 && (
            <Typography variant="body2" color="text.secondary">
              {agentNames[0]}
            </Typography>
          )}

          {/* Interaction count badges */}
          {counts.llm > 0 && (
            <Chip
              label={`${counts.llm} LLM`}
              size="small"
              color="primary"
              variant="outlined"
              sx={{ fontSize: '0.65rem', height: 20, fontWeight: 600 }}
            />
          )}
          {counts.mcp > 0 && (
            <Chip
              label={`${counts.mcp} MCP`}
              size="small"
              color="secondary"
              variant="outlined"
              sx={{ fontSize: '0.65rem', height: 20, fontWeight: 600 }}
            />
          )}

          {/* Status chip */}
          <Chip
            label={getStageStatusDisplayName(status)}
            size="small"
            color={statusColor === 'default' ? undefined : statusColor}
            sx={{ fontWeight: 600, fontSize: '0.75rem', ml: 'auto' }}
          />

          {/* Duration chip */}
          {duration != null && (
            <Chip
              label={formatDurationMs(duration)}
              size="small"
              variant="outlined"
              sx={{ fontSize: '0.75rem' }}
            />
          )}
        </Box>
      </AccordionSummary>

      <AccordionDetails sx={{ pt: 2 }}>
        {isParallel ? (
          <ParallelExecutionTabs stage={stage} session={session} />
        ) : (
          <Box>
            {/* Single agent metadata */}
            {singleOverview && (
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
                      <strong>Agent:</strong> {singleOverview.agent_name}
                    </Typography>
                    {singleOverview.llm_backend && (
                      <Typography variant="body2" color="text.secondary">
                        <strong>Backend:</strong> {singleOverview.llm_backend}
                      </Typography>
                    )}
                    {singleOverview.llm_provider && (
                      <Typography variant="body2" color="text.secondary">
                        <strong>Provider:</strong> {singleOverview.llm_provider}
                      </Typography>
                    )}
                    <Typography variant="body2" color="text.secondary">
                      <strong>Interactions:</strong> {counts.total}
                    </Typography>
                  </Box>
                  <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 2 }}>
                    {singleOverview.started_at && (
                      <Typography variant="body2" color="text.secondary">
                        <strong>Started:</strong> {formatTimestamp(singleOverview.started_at, 'short')}
                      </Typography>
                    )}
                    {singleOverview.completed_at && (
                      <Typography variant="body2" color="text.secondary">
                        <strong>Completed:</strong> {formatTimestamp(singleOverview.completed_at, 'short')}
                      </Typography>
                    )}
                    {singleOverview.duration_ms != null && (
                      <Typography variant="body2" color="text.secondary">
                        <strong>Duration:</strong> {formatDurationMs(singleOverview.duration_ms)}
                      </Typography>
                    )}
                  </Box>

                  {singleOverview.total_tokens > 0 && (
                    <TokenUsageDisplay
                      tokenData={{
                        input_tokens: singleOverview.input_tokens,
                        output_tokens: singleOverview.output_tokens,
                        total_tokens: singleOverview.total_tokens,
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

            {/* Error */}
            {singleOverview?.error_message && (
              <Alert severity="error" sx={{ mb: 2 }}>
                <Typography variant="body2">{singleOverview.error_message}</Typography>
              </Alert>
            )}

            {/* Interaction list */}
            {singleInteractions.length > 0 ? (
              <Stack spacing={2}>
                {singleInteractions.map((interaction) => (
                  <InteractionCard
                    key={interaction.id}
                    interaction={interaction}
                    sessionId={session.id}
                  />
                ))}
              </Stack>
            ) : (
              <Typography variant="body2" color="text.secondary" sx={{ py: 2, textAlign: 'center' }}>
                No interactions recorded for this stage
              </Typography>
            )}
          </Box>
        )}
      </AccordionDetails>
    </Accordion>
  );
}
