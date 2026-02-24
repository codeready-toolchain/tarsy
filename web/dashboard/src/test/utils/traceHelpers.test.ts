/**
 * Tests for traceHelpers.ts
 *
 * Covers: serializeMessageContent, mergeAndSortInteractions, countStageInteractions,
 *         findStageOverview, findExecutionOverview, computeStageDuration,
 *         getAggregateStatus, getExecutionStatusCounts, getAggregateTotalTokens,
 *         getAggregateDuration, formatLLMDetailForCopy, formatMCPDetailForCopy
 */

import {
  serializeMessageContent,
  mergeAndSortInteractions,
  countStageInteractions,
  findStageOverview,
  findExecutionOverview,
  computeStageDuration,
  getAggregateStatus,
  getExecutionStatusCounts,
  getAggregateTotalTokens,
  getAggregateDuration,
  formatLLMDetailForCopy,
  formatMCPDetailForCopy,
  isParallelStage,
} from '../../components/trace/traceHelpers';
import type {
  TraceExecutionGroup,
  TraceStageGroup,
  LLMInteractionDetailResponse,
  MCPInteractionDetailResponse,
} from '../../types/trace';
import type { SessionDetailResponse, StageOverview, ExecutionOverview } from '../../types/session';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';

// ---------------------------------------------------------------------------
// Factories
// ---------------------------------------------------------------------------

function makeExecution(overrides?: Partial<TraceExecutionGroup>): TraceExecutionGroup {
  return {
    execution_id: 'exec-1',
    agent_name: 'TestAgent',
    llm_interactions: [],
    mcp_interactions: [],
    ...overrides,
  };
}

function makeStageGroup(overrides?: Partial<TraceStageGroup>): TraceStageGroup {
  return {
    stage_id: 'stage-1',
    stage_name: 'Investigation',
    executions: [makeExecution()],
    ...overrides,
  };
}

function makeExecutionOverview(overrides?: Partial<ExecutionOverview>): ExecutionOverview {
  return {
    execution_id: 'exec-1',
    agent_name: 'TestAgent',
    agent_index: 0,
    status: EXECUTION_STATUS.COMPLETED,
    llm_backend: 'google-native',
    llm_provider: 'google',
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:01:00Z',
    duration_ms: 60000,
    error_message: null,
    input_tokens: 1000,
    output_tokens: 500,
    total_tokens: 1500,
    ...overrides,
  };
}

function makeStageOverview(overrides?: Partial<StageOverview>): StageOverview {
  return {
    id: 'stage-1',
    stage_name: 'Investigation',
    stage_index: 0,
    status: EXECUTION_STATUS.COMPLETED,
    parallel_type: null,
    expected_agent_count: 1,
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:05:00Z',
    ...overrides,
  };
}

function makeSessionDetail(stages: StageOverview[]): SessionDetailResponse {
  return {
    id: 'session-1',
    alert_data: '{}',
    alert_type: 'test',
    status: 'completed',
    chain_id: 'test-chain',
    author: null,
    error_message: null,
    final_analysis: null,
    executive_summary: null,
    executive_summary_error: null,
    runbook_url: null,
    created_at: '2025-01-15T10:00:00Z',
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:05:00Z',
    duration_ms: 300000,
    chat_enabled: false,
    chat_id: null,
    chat_message_count: 0,
    total_stages: stages.length,
    completed_stages: stages.length,
    failed_stages: 0,
    has_parallel_stages: false,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    llm_interaction_count: 0,
    mcp_interaction_count: 0,
    current_stage_index: null,
    current_stage_id: null,
    stages,
  };
}

// ---------------------------------------------------------------------------
// serializeMessageContent
// ---------------------------------------------------------------------------

describe('serializeMessageContent', () => {
  it('returns string content as-is', () => {
    expect(serializeMessageContent('hello')).toBe('hello');
  });

  it('returns empty string for null', () => {
    expect(serializeMessageContent(null)).toBe('');
  });

  it('returns empty string for undefined', () => {
    expect(serializeMessageContent(undefined)).toBe('');
  });

  it('returns empty string for empty string', () => {
    expect(serializeMessageContent('')).toBe('');
  });

  it('JSON-stringifies objects', () => {
    expect(serializeMessageContent({ key: 'value' })).toBe('{"key":"value"}');
  });

  it('JSON-stringifies arrays', () => {
    expect(serializeMessageContent([1, 2, 3])).toBe('[1,2,3]');
  });
});

// ---------------------------------------------------------------------------
// mergeAndSortInteractions
// ---------------------------------------------------------------------------

describe('mergeAndSortInteractions', () => {
  it('merges LLM and MCP interactions chronologically', () => {
    const exec = makeExecution({
      llm_interactions: [
        { id: 'llm-1', interaction_type: 'iteration', model_name: 'gemini', created_at: '2025-01-15T10:00:00Z' },
        { id: 'llm-2', interaction_type: 'final_analysis', model_name: 'gemini', created_at: '2025-01-15T10:02:00Z' },
      ],
      mcp_interactions: [
        { id: 'mcp-1', interaction_type: 'tool_call', server_name: 'k8s', tool_name: 'get_pods', created_at: '2025-01-15T10:01:00Z' },
      ],
    });

    const result = mergeAndSortInteractions(exec);
    expect(result).toHaveLength(3);
    expect(result[0].id).toBe('llm-1');
    expect(result[0].kind).toBe('llm');
    expect(result[1].id).toBe('mcp-1');
    expect(result[1].kind).toBe('mcp');
    expect(result[2].id).toBe('llm-2');
  });

  it('returns empty array for no interactions', () => {
    const exec = makeExecution();
    expect(mergeAndSortInteractions(exec)).toHaveLength(0);
  });

  it('preserves LLM-specific fields', () => {
    const exec = makeExecution({
      llm_interactions: [
        { id: 'llm-1', interaction_type: 'iteration', model_name: 'gemini', created_at: '2025-01-15T10:00:00Z', total_tokens: 1500 },
      ],
    });
    const result = mergeAndSortInteractions(exec);
    expect(result[0].model_name).toBe('gemini');
    expect(result[0].total_tokens).toBe(1500);
  });

  it('preserves MCP-specific fields', () => {
    const exec = makeExecution({
      mcp_interactions: [
        { id: 'mcp-1', interaction_type: 'tool_call', server_name: 'k8s', tool_name: 'get_pods', created_at: '2025-01-15T10:00:00Z' },
      ],
    });
    const result = mergeAndSortInteractions(exec);
    expect(result[0].server_name).toBe('k8s');
    expect(result[0].tool_name).toBe('get_pods');
  });
});

// ---------------------------------------------------------------------------
// countStageInteractions
// ---------------------------------------------------------------------------

describe('countStageInteractions', () => {
  it('counts interactions across all executions', () => {
    const stage = makeStageGroup({
      executions: [
        makeExecution({
          llm_interactions: [
            { id: 'l1', interaction_type: 'iteration', model_name: 'g', created_at: '' },
            { id: 'l2', interaction_type: 'iteration', model_name: 'g', created_at: '' },
          ],
          mcp_interactions: [
            { id: 'm1', interaction_type: 'tool_call', server_name: 's', created_at: '' },
          ],
        }),
        makeExecution({
          llm_interactions: [
            { id: 'l3', interaction_type: 'iteration', model_name: 'g', created_at: '' },
          ],
          mcp_interactions: [],
        }),
      ],
    });
    const counts = countStageInteractions(stage);
    expect(counts.llm).toBe(3);
    expect(counts.mcp).toBe(1);
    expect(counts.total).toBe(4);
  });
});

// ---------------------------------------------------------------------------
// Session detail lookups
// ---------------------------------------------------------------------------

describe('findStageOverview', () => {
  it('finds stage by id', () => {
    const stage = makeStageOverview({ id: 'stage-1' });
    const session = makeSessionDetail([stage]);
    expect(findStageOverview(session, 'stage-1')).toBe(stage);
  });

  it('returns undefined for unknown id', () => {
    const session = makeSessionDetail([makeStageOverview({ id: 'stage-1' })]);
    expect(findStageOverview(session, 'nonexistent')).toBeUndefined();
  });
});

describe('findExecutionOverview', () => {
  it('finds execution across stages', () => {
    const exec = makeExecutionOverview({ execution_id: 'exec-2' });
    const stage = makeStageOverview({ id: 'stage-1', executions: [makeExecutionOverview(), exec] });
    const session = makeSessionDetail([stage]);
    expect(findExecutionOverview(session, 'exec-2')).toBe(exec);
  });

  it('returns undefined for unknown execution', () => {
    const stage = makeStageOverview({ id: 'stage-1', executions: [makeExecutionOverview()] });
    const session = makeSessionDetail([stage]);
    expect(findExecutionOverview(session, 'nonexistent')).toBeUndefined();
  });
});

describe('computeStageDuration', () => {
  it('computes duration from timestamps', () => {
    const stage = makeStageOverview({
      started_at: '2025-01-15T10:00:00Z',
      completed_at: '2025-01-15T10:05:00Z',
    });
    expect(computeStageDuration(stage)).toBe(300000);
  });

  it('returns null when started_at is missing', () => {
    const stage = makeStageOverview({ started_at: null });
    expect(computeStageDuration(stage)).toBeNull();
  });

  it('returns null for undefined input', () => {
    expect(computeStageDuration(undefined)).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Parallel stage helpers
// ---------------------------------------------------------------------------

describe('isParallelStage', () => {
  it('returns true when executions > 1', () => {
    const stage = makeStageGroup({ executions: [makeExecution(), makeExecution()] });
    expect(isParallelStage(stage)).toBe(true);
  });

  it('returns true when stageOverview has parallel_type', () => {
    const stage = makeStageGroup();
    const overview = makeStageOverview({ parallel_type: 'multi_agent' });
    expect(isParallelStage(stage, overview)).toBe(true);
  });

  it('returns false for single execution without parallel_type', () => {
    const stage = makeStageGroup();
    expect(isParallelStage(stage)).toBe(false);
  });
});

describe('getAggregateStatus', () => {
  it('returns "All Completed" when all complete', () => {
    const execs = [
      makeExecutionOverview({ status: EXECUTION_STATUS.COMPLETED }),
      makeExecutionOverview({ status: EXECUTION_STATUS.COMPLETED }),
    ];
    expect(getAggregateStatus(execs)).toBe('All Completed');
  });

  it('returns "All Failed" when all failed', () => {
    const execs = [
      makeExecutionOverview({ status: EXECUTION_STATUS.FAILED }),
      makeExecutionOverview({ status: EXECUTION_STATUS.TIMED_OUT }),
    ];
    expect(getAggregateStatus(execs)).toBe('All Failed');
  });

  it('shows running count when active', () => {
    const execs = [
      makeExecutionOverview({ status: EXECUTION_STATUS.ACTIVE }),
      makeExecutionOverview({ status: EXECUTION_STATUS.COMPLETED }),
      makeExecutionOverview({ status: EXECUTION_STATUS.PENDING }),
    ];
    expect(getAggregateStatus(execs)).toBe('1/3 Running');
  });

  it('shows completed fraction for mixed terminal', () => {
    const execs = [
      makeExecutionOverview({ status: EXECUTION_STATUS.COMPLETED }),
      makeExecutionOverview({ status: EXECUTION_STATUS.FAILED }),
      makeExecutionOverview({ status: EXECUTION_STATUS.COMPLETED }),
    ];
    expect(getAggregateStatus(execs)).toBe('2/3 Completed');
  });
});

describe('getExecutionStatusCounts', () => {
  it('counts all status categories', () => {
    const execs = [
      makeExecutionOverview({ status: EXECUTION_STATUS.COMPLETED }),
      makeExecutionOverview({ status: EXECUTION_STATUS.FAILED }),
      makeExecutionOverview({ status: EXECUTION_STATUS.TIMED_OUT }),
      makeExecutionOverview({ status: EXECUTION_STATUS.ACTIVE }),
      makeExecutionOverview({ status: EXECUTION_STATUS.PENDING }),
      makeExecutionOverview({ status: EXECUTION_STATUS.CANCELLED }),
    ];
    const counts = getExecutionStatusCounts(execs);
    expect(counts.completed).toBe(1);
    expect(counts.failed).toBe(2); // failed + timed_out
    expect(counts.active).toBe(1);
    expect(counts.pending).toBe(1);
    expect(counts.cancelled).toBe(1);
  });
});

describe('getAggregateTotalTokens', () => {
  it('sums tokens across executions', () => {
    const execs = [
      makeExecutionOverview({ input_tokens: 100, output_tokens: 50, total_tokens: 150 }),
      makeExecutionOverview({ input_tokens: 200, output_tokens: 100, total_tokens: 300 }),
    ];
    const result = getAggregateTotalTokens(execs);
    expect(result.input_tokens).toBe(300);
    expect(result.output_tokens).toBe(150);
    expect(result.total_tokens).toBe(450);
  });
});

describe('getAggregateDuration', () => {
  it('returns max duration', () => {
    const execs = [
      makeExecutionOverview({ duration_ms: 5000 }),
      makeExecutionOverview({ duration_ms: 10000 }),
      makeExecutionOverview({ duration_ms: 3000 }),
    ];
    expect(getAggregateDuration(execs)).toBe(10000);
  });

  it('returns null when no durations available', () => {
    const execs = [
      makeExecutionOverview({ duration_ms: null }),
      makeExecutionOverview({ duration_ms: null }),
    ];
    expect(getAggregateDuration(execs)).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Copy formatting
// ---------------------------------------------------------------------------

describe('formatLLMDetailForCopy', () => {
  it('formats LLM conversation for clipboard', () => {
    const detail: LLMInteractionDetailResponse = {
      id: 'llm-1',
      interaction_type: 'iteration',
      model_name: 'gemini-2.0-flash',
      total_tokens: 5000,
      duration_ms: 12000,
      conversation: [
        { role: 'system', content: 'You are a helpful assistant' },
        { role: 'user', content: 'Investigate the pod crash' },
        {
          role: 'assistant',
          content: 'Let me check the logs',
          tool_calls: [{ id: 'tc-1', name: 'get_logs', arguments: '{"pod": "nginx"}' }],
        },
      ],
      llm_request: {},
      llm_response: {},
      created_at: '2025-01-15T10:00:00Z',
    };

    const text = formatLLMDetailForCopy(detail);
    expect(text).toContain('=== LLM CONVERSATION ===');
    expect(text).toContain('SYSTEM:');
    expect(text).toContain('You are a helpful assistant');
    expect(text).toContain('USER:');
    expect(text).toContain('ASSISTANT:');
    expect(text).toContain('[Tool Call] get_logs');
    expect(text).toContain('Model: gemini-2.0-flash');
    expect(text).toContain('Tokens: 5,000');
  });

  it('handles structured content in messages', () => {
    const detail: LLMInteractionDetailResponse = {
      id: 'llm-1',
      interaction_type: 'iteration',
      model_name: 'model',
      conversation: [{ role: 'assistant', content: { parts: ['text'] } as unknown as string }],
      llm_request: {},
      llm_response: {},
      created_at: '2025-01-15T10:00:00Z',
    };

    const text = formatLLMDetailForCopy(detail);
    // Should not show [object Object]
    expect(text).not.toContain('[object Object]');
    expect(text).toContain('parts');
  });
});

describe('formatMCPDetailForCopy', () => {
  it('formats tool call details', () => {
    const detail: MCPInteractionDetailResponse = {
      id: 'mcp-1',
      interaction_type: 'tool_call',
      server_name: 'kubernetes',
      tool_name: 'get_pods',
      tool_arguments: { namespace: 'default' },
      tool_result: { pods: ['nginx'] },
      duration_ms: 500,
      created_at: '2025-01-15T10:00:00Z',
    };

    const text = formatMCPDetailForCopy(detail);
    expect(text).toContain('=== MCP TOOL CALL ===');
    expect(text).toContain('SERVER: kubernetes');
    expect(text).toContain('TOOL: get_pods');
    expect(text).toContain('PARAMETERS:');
    expect(text).toContain('"namespace": "default"');
    expect(text).toContain('RESULT:');
  });

  it('formats tool list details', () => {
    const detail: MCPInteractionDetailResponse = {
      id: 'mcp-1',
      interaction_type: 'tool_list',
      server_name: 'kubernetes',
      available_tools: [
        { name: 'get_pods', description: 'List pods' },
        { name: 'get_logs', description: 'Get pod logs' },
      ],
      created_at: '2025-01-15T10:00:00Z',
    };

    const text = formatMCPDetailForCopy(detail);
    expect(text).toContain('=== MCP TOOL LIST ===');
    expect(text).toContain('AVAILABLE TOOLS (2)');
    expect(text).toContain('- get_pods: List pods');
    expect(text).toContain('- get_logs: Get pod logs');
  });

  it('formats tool_call with list_tools name as tool list', () => {
    const detail: MCPInteractionDetailResponse = {
      id: 'mcp-1',
      interaction_type: 'tool_call',
      server_name: 'k8s',
      tool_name: 'list_tools',
      available_tools: [{ name: 'tool1', description: 'desc' }],
      created_at: '2025-01-15T10:00:00Z',
    };

    const text = formatMCPDetailForCopy(detail);
    expect(text).toContain('=== MCP TOOL LIST ===');
  });

  it('includes error message when present', () => {
    const detail: MCPInteractionDetailResponse = {
      id: 'mcp-1',
      interaction_type: 'tool_call',
      server_name: 'k8s',
      tool_name: 'get_pods',
      error_message: 'connection refused',
      created_at: '2025-01-15T10:00:00Z',
    };

    const text = formatMCPDetailForCopy(detail);
    expect(text).toContain('ERROR: connection refused');
  });
});
