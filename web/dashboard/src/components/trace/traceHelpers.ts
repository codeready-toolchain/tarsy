/**
 * Shared helper functions for the trace view components.
 *
 * Status colors/icons, interaction type labels, step descriptions,
 * merge+sort interactions, and copy formatting.
 */

import type { ReactElement } from 'react';
import { createElement } from 'react';
import {
  CheckCircle,
  Error as ErrorIcon,
  PlayArrow,
  Schedule,
  Cancel,
  TimerOff,
} from '@mui/icons-material';

import type {
  TraceListResponse,
  TraceStageGroup,
  TraceExecutionGroup,
  LLMInteractionListItem,
  MCPInteractionListItem,
  LLMInteractionDetailResponse,
  MCPInteractionDetailResponse,
} from '../../types/trace';
import type { SessionDetailResponse, StageOverview, ExecutionOverview } from '../../types/session';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';
import { LLM_INTERACTION_TYPE, MCP_INTERACTION_TYPE, MCP_LIST_TOOLS_NAME } from '../../constants/interactionTypes';
import { formatDurationMs, formatTimestamp, formatTokensCompact } from '../../utils/format';

// ────────────────────────────────────────────────────────────
// Content serialization
// ────────────────────────────────────────────────────────────

/**
 * Serialize message content to a display-safe string.
 *
 * The TypeScript type says `content: string`, but the API may return
 * structured content (objects/arrays) for certain LLM responses.
 * This prevents `[object Object]` in display and copy operations.
 */
export function serializeMessageContent(content: unknown): string {
  if (typeof content === 'string') return content;
  if (content == null || content === '') return '';
  return JSON.stringify(content);
}

// ────────────────────────────────────────────────────────────
// Interaction type labels and colors
// ────────────────────────────────────────────────────────────

/** Map backend interaction_type to human-readable label. */
export function getInteractionTypeLabel(type: string): string {
  switch (type) {
    case LLM_INTERACTION_TYPE.ITERATION:
      return 'Investigation';
    case LLM_INTERACTION_TYPE.SUMMARIZATION:
      return 'Summarization';
    case LLM_INTERACTION_TYPE.FINAL_ANALYSIS:
      return 'Final Analysis';
    case LLM_INTERACTION_TYPE.EXECUTIVE_SUMMARY:
      return 'Executive Summary';
    case LLM_INTERACTION_TYPE.CHAT_RESPONSE:
      return 'Chat Response';
    case MCP_INTERACTION_TYPE.TOOL_CALL:
      return 'Tool Call';
    case MCP_INTERACTION_TYPE.TOOL_LIST:
      return 'Tool List';
    default:
      return type.charAt(0).toUpperCase() + type.slice(1).replace(/_/g, ' ');
  }
}

/** Map interaction_type to MUI color for Chip/border. */
export function getInteractionTypeColor(
  type: string,
): 'primary' | 'warning' | 'success' | 'info' | 'secondary' | 'error' | 'default' {
  switch (type) {
    case LLM_INTERACTION_TYPE.ITERATION:
      return 'primary';
    case LLM_INTERACTION_TYPE.SUMMARIZATION:
      return 'warning';
    case LLM_INTERACTION_TYPE.FINAL_ANALYSIS:
      return 'success';
    case LLM_INTERACTION_TYPE.EXECUTIVE_SUMMARY:
      return 'info';
    case LLM_INTERACTION_TYPE.CHAT_RESPONSE:
      return 'primary';
    case MCP_INTERACTION_TYPE.TOOL_CALL:
      return 'secondary';
    case MCP_INTERACTION_TYPE.TOOL_LIST:
      return 'secondary';
    default:
      return 'default';
  }
}

/** Color key for the card kind ('llm' or 'mcp'). */
export function getCardColorKey(
  kind: 'llm' | 'mcp',
  interactionType?: string,
): 'primary' | 'warning' | 'success' | 'info' | 'secondary' {
  if (kind === 'mcp') return 'secondary';
  if (!interactionType) return 'primary';
  const c = getInteractionTypeColor(interactionType);
  return c === 'default' || c === 'error' ? 'primary' : c;
}

// ────────────────────────────────────────────────────────────
// Stage status helpers
// ────────────────────────────────────────────────────────────

/** Stage status to MUI palette key. */
export function getStageStatusColor(
  status: string,
): 'success' | 'error' | 'primary' | 'warning' | 'default' {
  switch (status) {
    case EXECUTION_STATUS.COMPLETED:
      return 'success';
    case EXECUTION_STATUS.FAILED:
    case EXECUTION_STATUS.TIMED_OUT:
      return 'error';
    case EXECUTION_STATUS.ACTIVE:
    case EXECUTION_STATUS.STARTED:
      return 'primary';
    case EXECUTION_STATUS.CANCELLED:
      return 'warning';
    case EXECUTION_STATUS.PENDING:
    default:
      return 'default';
  }
}

/** Stage status to icon element. */
export function getStageStatusIcon(status: string): ReactElement {
  switch (status) {
    case EXECUTION_STATUS.COMPLETED:
      return createElement(CheckCircle, { fontSize: 'small' });
    case EXECUTION_STATUS.FAILED:
      return createElement(ErrorIcon, { fontSize: 'small' });
    case EXECUTION_STATUS.TIMED_OUT:
      return createElement(TimerOff, { fontSize: 'small' });
    case EXECUTION_STATUS.ACTIVE:
    case EXECUTION_STATUS.STARTED:
      return createElement(PlayArrow, { fontSize: 'small' });
    case EXECUTION_STATUS.CANCELLED:
      return createElement(Cancel, { fontSize: 'small' });
    case EXECUTION_STATUS.PENDING:
    default:
      return createElement(Schedule, { fontSize: 'small' });
  }
}

/** Human-readable display name for stage/execution status. */
export function getStageStatusDisplayName(status: string): string {
  switch (status) {
    case EXECUTION_STATUS.COMPLETED:
      return 'Completed';
    case EXECUTION_STATUS.FAILED:
      return 'Failed';
    case EXECUTION_STATUS.TIMED_OUT:
      return 'Timed Out';
    case EXECUTION_STATUS.ACTIVE:
      return 'Active';
    case EXECUTION_STATUS.STARTED:
      return 'Started';
    case EXECUTION_STATUS.CANCELLED:
      return 'Cancelled';
    case EXECUTION_STATUS.PENDING:
      return 'Pending';
    default:
      return status;
  }
}

// ────────────────────────────────────────────────────────────
// Step description builders
// ────────────────────────────────────────────────────────────

/** Build a human-readable step description for an LLM interaction. */
export function computeLLMStepDescription(interaction: LLMInteractionListItem): string {
  const label = getInteractionTypeLabel(interaction.interaction_type);
  if (interaction.model_name) {
    return `${label} — ${interaction.model_name}`;
  }
  return label;
}

/** Build a human-readable step description for an MCP interaction. */
export function computeMCPStepDescription(interaction: MCPInteractionListItem): string {
  if (interaction.interaction_type === MCP_INTERACTION_TYPE.TOOL_LIST) {
    return `Tool List — ${interaction.server_name}`;
  }
  if (interaction.tool_name) {
    return `${interaction.server_name}.${interaction.tool_name}`;
  }
  return `MCP — ${interaction.server_name}`;
}

// ────────────────────────────────────────────────────────────
// Merge and sort
// ────────────────────────────────────────────────────────────

export interface UnifiedInteraction {
  id: string;
  kind: 'llm' | 'mcp';
  interaction_type: string;
  created_at: string;
  duration_ms?: number;
  error_message?: string;
  // LLM-specific
  model_name?: string;
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  // MCP-specific
  server_name?: string;
  tool_name?: string;
}

/** Merge LLM and MCP interaction lists into a single chronological list. */
export function mergeAndSortInteractions(execution: TraceExecutionGroup): UnifiedInteraction[] {
  const llm: UnifiedInteraction[] = execution.llm_interactions.map((i) => ({
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
  }));

  const mcp: UnifiedInteraction[] = execution.mcp_interactions.map((i) => ({
    id: i.id,
    kind: 'mcp' as const,
    interaction_type: i.interaction_type,
    created_at: i.created_at,
    duration_ms: i.duration_ms,
    error_message: i.error_message,
    server_name: i.server_name,
    tool_name: i.tool_name,
  }));

  return [...llm, ...mcp].sort(
    (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
  );
}

/** Count total interactions across all executions in a stage. */
export function countStageInteractions(stage: TraceStageGroup): {
  total: number;
  llm: number;
  mcp: number;
} {
  let llm = 0;
  let mcp = 0;
  for (const exec of stage.executions) {
    llm += exec.llm_interactions.length;
    mcp += exec.mcp_interactions.length;
  }
  return { total: llm + mcp, llm, mcp };
}

// ────────────────────────────────────────────────────────────
// Session detail lookups
// ────────────────────────────────────────────────────────────

/** Find the StageOverview from session detail for a trace stage. */
export function findStageOverview(
  session: SessionDetailResponse,
  stageId: string,
): StageOverview | undefined {
  return session.stages?.find((s) => s.id === stageId);
}

/** Find the ExecutionOverview from session detail for a trace execution. */
export function findExecutionOverview(
  session: SessionDetailResponse,
  executionId: string,
): ExecutionOverview | undefined {
  for (const stage of session.stages ?? []) {
    const exec = stage.executions?.find((e) => e.execution_id === executionId);
    if (exec) return exec;
  }
  return undefined;
}

/** Compute stage duration from StageOverview timestamps. */
export function computeStageDuration(stageOverview: StageOverview | undefined): number | null {
  if (!stageOverview?.started_at || !stageOverview?.completed_at) return null;
  return new Date(stageOverview.completed_at).getTime() - new Date(stageOverview.started_at).getTime();
}

// ────────────────────────────────────────────────────────────
// Parallel stage helpers
// ────────────────────────────────────────────────────────────

/** Check whether a trace stage has parallel executions. */
export function isParallelStage(stage: TraceStageGroup, stageOverview?: StageOverview): boolean {
  if (stage.executions.length > 1) return true;
  if (stageOverview?.parallel_type) return true;
  return false;
}

/** Compute aggregate status label for parallel executions. */
export function getAggregateStatus(
  executions: ExecutionOverview[],
): string {
  const total = executions.length;
  const completed = executions.filter((e) => e.status === EXECUTION_STATUS.COMPLETED).length;
  const failed = executions.filter(
    (e) => e.status === EXECUTION_STATUS.FAILED || e.status === EXECUTION_STATUS.TIMED_OUT,
  ).length;
  const active = executions.filter(
    (e) => e.status === EXECUTION_STATUS.ACTIVE || e.status === EXECUTION_STATUS.STARTED,
  ).length;

  if (completed === total) return 'All Completed';
  if (failed === total) return 'All Failed';
  if (active > 0) return `${active}/${total} Running`;
  return `${completed}/${total} Completed`;
}

/** Count execution statuses for the summary. */
export function getExecutionStatusCounts(executions: ExecutionOverview[]): {
  completed: number;
  failed: number;
  active: number;
  pending: number;
  cancelled: number;
} {
  return {
    completed: executions.filter((e) => e.status === EXECUTION_STATUS.COMPLETED).length,
    failed: executions.filter(
      (e) => e.status === EXECUTION_STATUS.FAILED || e.status === EXECUTION_STATUS.TIMED_OUT,
    ).length,
    active: executions.filter(
      (e) => e.status === EXECUTION_STATUS.ACTIVE || e.status === EXECUTION_STATUS.STARTED,
    ).length,
    pending: executions.filter((e) => e.status === EXECUTION_STATUS.PENDING).length,
    cancelled: executions.filter((e) => e.status === EXECUTION_STATUS.CANCELLED).length,
  };
}

/** Aggregate token usage across parallel executions (from session detail). */
export function getAggregateTotalTokens(
  executions: ExecutionOverview[],
): { input_tokens: number; output_tokens: number; total_tokens: number } {
  let input = 0;
  let output = 0;
  let total = 0;
  for (const exec of executions) {
    input += exec.input_tokens ?? 0;
    output += exec.output_tokens ?? 0;
    total += exec.total_tokens ?? 0;
  }
  return { input_tokens: input, output_tokens: output, total_tokens: total };
}

/** Aggregate duration across parallel executions. */
export function getAggregateDuration(executions: ExecutionOverview[]): number | null {
  const durations = executions
    .map((e) => e.duration_ms)
    .filter((d): d is number => d != null);
  if (durations.length === 0) return null;
  return Math.max(...durations);
}

// ────────────────────────────────────────────────────────────
// Copy formatting
// ────────────────────────────────────────────────────────────

/** Format entire trace flow for clipboard. */
export function formatEntireFlowForCopy(
  traceData: TraceListResponse,
  session: SessionDetailResponse,
): string {
  let content = `====== CHAIN EXECUTION: ${session.chain_id} ======\n`;
  content += `Total Stages: ${traceData.stages.length}\n`;

  const completedStages = traceData.stages.filter((s) => {
    const overview = findStageOverview(session, s.stage_id);
    return overview?.status === EXECUTION_STATUS.COMPLETED;
  }).length;
  const failedStages = traceData.stages.filter((s) => {
    const overview = findStageOverview(session, s.stage_id);
    return overview?.status === EXECUTION_STATUS.FAILED || overview?.status === EXECUTION_STATUS.TIMED_OUT;
  }).length;

  content += `Completed: ${completedStages}\n`;
  content += `Failed: ${failedStages}\n\n`;

  traceData.stages.forEach((stage, stageIndex) => {
    content += `\n${'='.repeat(80)}\n`;
    content += formatStageForCopy(stage, stageIndex, session);
    if (stageIndex < traceData.stages.length - 1) {
      content += `${'='.repeat(80)}\n`;
    }
  });

  if (traceData.session_interactions.length > 0) {
    content += `\n${'='.repeat(80)}\n`;
    content += `SESSION-LEVEL INTERACTIONS\n`;
    content += `${'='.repeat(80)}\n`;
    for (const interaction of traceData.session_interactions) {
      content += formatLLMInteractionForCopy(interaction);
      content += '\n';
    }
  }

  return content;
}

/** Format a single stage for clipboard. */
export function formatStageForCopy(
  stage: TraceStageGroup,
  stageIndex: number,
  session: SessionDetailResponse,
): string {
  const overview = findStageOverview(session, stage.stage_id);
  const counts = countStageInteractions(stage);

  let content = `STAGE ${stageIndex + 1}: ${stage.stage_name}\n`;
  content += `Status: ${overview?.status ?? 'unknown'}\n`;
  content += `Interactions: ${counts.total} (LLM: ${counts.llm}, MCP: ${counts.mcp})\n`;

  if (overview?.started_at) {
    content += `Started: ${formatTimestamp(overview.started_at, 'absolute')}\n`;
  }

  const duration = computeStageDuration(overview);
  if (duration != null) {
    content += `Duration: ${formatDurationMs(duration)}\n`;
  }

  content += '\n';

  for (const exec of stage.executions) {
    if (stage.executions.length > 1) {
      content += `  --- Agent: ${exec.agent_name} ---\n`;
    }
    const interactions = mergeAndSortInteractions(exec);
    for (const interaction of interactions) {
      if (interaction.kind === 'llm') {
        content += `  [LLM] ${computeLLMStepDescription(interaction as LLMInteractionListItem)}`;
      } else {
        content += `  [MCP] ${computeMCPStepDescription(interaction as MCPInteractionListItem)}`;
      }
      if (interaction.duration_ms != null) {
        content += ` (${formatDurationMs(interaction.duration_ms)})`;
      }
      if (interaction.error_message) {
        content += ` ERROR: ${interaction.error_message}`;
      }
      content += '\n';
    }
    content += '\n';
  }

  return content;
}

/** Format a single LLM interaction for copy. */
function formatLLMInteractionForCopy(interaction: LLMInteractionListItem): string {
  let content = `[LLM] ${getInteractionTypeLabel(interaction.interaction_type)}`;
  content += ` — ${interaction.model_name}`;
  if (interaction.total_tokens != null) {
    content += ` (${formatTokensCompact(interaction.total_tokens)} tokens)`;
  }
  if (interaction.duration_ms != null) {
    content += ` ${formatDurationMs(interaction.duration_ms)}`;
  }
  if (interaction.error_message) {
    content += ` ERROR: ${interaction.error_message}`;
  }
  return content;
}

/** Format LLM detail for "Copy All Details". */
export function formatLLMDetailForCopy(detail: LLMInteractionDetailResponse): string {
  let content = `=== LLM CONVERSATION ===\n\n`;

  for (const msg of detail.conversation) {
    content += `${msg.role.toUpperCase()}:\n`;
    content += `${serializeMessageContent(msg.content)}\n`;
    if (msg.tool_calls?.length) {
      for (const tc of msg.tool_calls) {
        content += `  [Tool Call] ${tc.name}(${tc.arguments})\n`;
      }
    }
    content += '\n';
  }

  content += `--- METADATA ---\n`;
  content += `Model: ${detail.model_name}\n`;
  content += `Type: ${getInteractionTypeLabel(detail.interaction_type)}\n`;
  if (detail.total_tokens != null) content += `Tokens: ${detail.total_tokens.toLocaleString()}\n`;
  if (detail.duration_ms != null) content += `Duration: ${formatDurationMs(detail.duration_ms)}\n`;

  return content;
}

/** Format MCP detail for "Copy All Details". */
export function formatMCPDetailForCopy(detail: MCPInteractionDetailResponse): string {
  const isToolList =
    detail.interaction_type === MCP_INTERACTION_TYPE.TOOL_LIST ||
    (detail.interaction_type === MCP_INTERACTION_TYPE.TOOL_CALL && detail.tool_name === MCP_LIST_TOOLS_NAME);

  let content = isToolList ? '=== MCP TOOL LIST ===\n\n' : '=== MCP TOOL CALL ===\n\n';

  content += `SERVER: ${detail.server_name}\n`;

  if (!isToolList) {
    content += `TOOL: ${detail.tool_name || 'unknown'}\n`;
    if (detail.duration_ms != null) content += `Duration: ${formatDurationMs(detail.duration_ms)}\n`;
    content += `\nPARAMETERS:\n${JSON.stringify(detail.tool_arguments, null, 2)}\n\n`;
    content += `RESULT:\n${JSON.stringify(detail.tool_result, null, 2)}`;
  } else if (detail.available_tools) {
    content += `\nAVAILABLE TOOLS (${detail.available_tools.length}):\n`;
    for (const t of detail.available_tools) {
      if (typeof t === 'object' && t !== null && 'name' in t) {
        const entry = t as { name: string; description?: string };
        content += `  - ${entry.name}: ${entry.description || '(no description)'}\n`;
      } else {
        content += `  - ${String(t)}\n`;
      }
    }
  }

  if (detail.error_message) {
    content += `\n\nERROR: ${detail.error_message}`;
  }

  return content;
}
