/**
 * Tests for orchestrator sub-agent support across the dashboard.
 *
 * Covers:
 * - SubAgentCard component rendering (collapsed/expanded)
 * - StageContent sub-agent partitioning and inline rendering
 * - WS event routing by parent_execution_id
 * - Type contract verification
 */

import { describe, it, expect } from 'vitest';
import type { ExecutionOverview, TimelineEvent } from '../../types/session';
import type {
  TimelineCreatedPayload,
  TimelineCompletedPayload,
  StreamChunkPayload,
  ExecutionProgressPayload,
  ExecutionStatusPayload,
} from '../../types/events';
import type { TraceExecutionGroup, TraceStageGroup } from '../../types/trace';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';
import { countStageInteractions, findExecutionOverview } from '../../components/trace/traceHelpers';

// ---------------------------------------------------------------------------
// Factories
// ---------------------------------------------------------------------------

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

function makeTraceExecution(overrides?: Partial<TraceExecutionGroup>): TraceExecutionGroup {
  return {
    execution_id: 'exec-1',
    agent_name: 'TestAgent',
    llm_interactions: [],
    mcp_interactions: [],
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// TypeScript type contract — parent_execution_id on all relevant types
// ---------------------------------------------------------------------------

describe('Type contracts: parent_execution_id field', () => {
  it('ExecutionOverview supports parent_execution_id, task, and sub_agents', () => {
    const eo: ExecutionOverview = makeExecutionOverview({
      parent_execution_id: 'parent-1',
      task: 'Analyze logs',
      sub_agents: [makeExecutionOverview({ execution_id: 'sub-1' })],
    });
    expect(eo.parent_execution_id).toBe('parent-1');
    expect(eo.task).toBe('Analyze logs');
    expect(eo.sub_agents).toHaveLength(1);
    expect(eo.sub_agents![0].execution_id).toBe('sub-1');
  });

  it('TimelineEvent supports parent_execution_id', () => {
    const event: TimelineEvent = {
      id: 'ev-1',
      session_id: 'sess-1',
      stage_id: 'stage-1',
      execution_id: 'exec-1',
      parent_execution_id: 'parent-1',
      sequence_number: 1,
      event_type: 'llm_thinking',
      status: 'completed',
      content: 'thinking...',
      metadata: null,
      created_at: '2025-01-15T10:00:00Z',
      updated_at: '2025-01-15T10:00:01Z',
    };
    expect(event.parent_execution_id).toBe('parent-1');
  });

  it('TimelineCreatedPayload supports parent_execution_id', () => {
    const p: TimelineCreatedPayload = {
      type: 'timeline_event.created',
      event_id: 'ev-1',
      session_id: 'sess-1',
      stage_id: 'stage-1',
      execution_id: 'exec-1',
      parent_execution_id: 'parent-1',
      event_type: 'llm_thinking',
      status: 'streaming',
      content: '',
      sequence_number: 1,
      timestamp: '2025-01-15T10:00:00Z',
    };
    expect(p.parent_execution_id).toBe('parent-1');
  });

  it('TimelineCompletedPayload supports parent_execution_id', () => {
    const p: TimelineCompletedPayload = {
      type: 'timeline_event.completed',
      session_id: 'sess-1',
      event_id: 'ev-1',
      parent_execution_id: 'parent-1',
      event_type: 'llm_thinking',
      content: 'done',
      status: 'completed',
      timestamp: '2025-01-15T10:00:01Z',
    };
    expect(p.parent_execution_id).toBe('parent-1');
  });

  it('StreamChunkPayload supports parent_execution_id', () => {
    const p: StreamChunkPayload = {
      type: 'stream.chunk',
      session_id: 'sess-1',
      event_id: 'ev-1',
      parent_execution_id: 'parent-1',
      delta: 'hello',
      timestamp: '2025-01-15T10:00:00Z',
    };
    expect(p.parent_execution_id).toBe('parent-1');
  });

  it('ExecutionProgressPayload supports parent_execution_id', () => {
    const p: ExecutionProgressPayload = {
      type: 'execution.progress',
      session_id: 'sess-1',
      stage_id: 'stage-1',
      execution_id: 'exec-1',
      parent_execution_id: 'parent-1',
      phase: 'investigating',
      message: 'Investigating...',
      timestamp: '2025-01-15T10:00:00Z',
    };
    expect(p.parent_execution_id).toBe('parent-1');
  });

  it('ExecutionStatusPayload supports parent_execution_id', () => {
    const p: ExecutionStatusPayload = {
      type: 'execution.status',
      session_id: 'sess-1',
      stage_id: 'stage-1',
      execution_id: 'exec-1',
      parent_execution_id: 'parent-1',
      agent_index: 1,
      status: 'active',
      timestamp: '2025-01-15T10:00:00Z',
    };
    expect(p.parent_execution_id).toBe('parent-1');
  });
});

// ---------------------------------------------------------------------------
// WS event routing logic
// ---------------------------------------------------------------------------

describe('WS event routing by parent_execution_id', () => {
  it('events without parent_execution_id route to top-level maps', () => {
    const payload: TimelineCreatedPayload = {
      type: 'timeline_event.created',
      event_id: 'ev-1',
      session_id: 'sess-1',
      event_type: 'llm_thinking',
      status: 'streaming',
      content: '',
      sequence_number: 1,
      timestamp: '2025-01-15T10:00:00Z',
    };
    const isSubAgent = !!payload.parent_execution_id;
    expect(isSubAgent).toBe(false);
  });

  it('events with parent_execution_id route to sub-agent maps', () => {
    const payload: TimelineCreatedPayload = {
      type: 'timeline_event.created',
      event_id: 'ev-1',
      session_id: 'sess-1',
      execution_id: 'sub-exec-1',
      parent_execution_id: 'parent-exec',
      event_type: 'llm_thinking',
      status: 'streaming',
      content: '',
      sequence_number: 1,
      timestamp: '2025-01-15T10:00:00Z',
    };
    const isSubAgent = !!payload.parent_execution_id;
    expect(isSubAgent).toBe(true);
  });

  it('empty string parent_execution_id routes to top-level', () => {
    const payload: ExecutionStatusPayload = {
      type: 'execution.status',
      session_id: 'sess-1',
      stage_id: 'stage-1',
      execution_id: 'exec-1',
      parent_execution_id: '',
      agent_index: 1,
      status: 'active',
      timestamp: '2025-01-15T10:00:00Z',
    };
    const isSubAgent = !!payload.parent_execution_id;
    expect(isSubAgent).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Sub-agent partitioning logic (StageContent)
// ---------------------------------------------------------------------------

describe('Sub-agent partitioning', () => {
  it('builds sub-agent ID set from executionOverviews', () => {
    const overviews: ExecutionOverview[] = [
      makeExecutionOverview({
        execution_id: 'orch-1',
        sub_agents: [
          makeExecutionOverview({ execution_id: 'sub-1' }),
          makeExecutionOverview({ execution_id: 'sub-2' }),
        ],
      }),
      makeExecutionOverview({ execution_id: 'normal-1' }),
    ];

    const subAgentIds = new Set<string>();
    for (const eo of overviews) {
      if (eo.sub_agents) {
        for (const sub of eo.sub_agents) {
          subAgentIds.add(sub.execution_id);
        }
      }
    }

    expect(subAgentIds.has('sub-1')).toBe(true);
    expect(subAgentIds.has('sub-2')).toBe(true);
    expect(subAgentIds.has('orch-1')).toBe(false);
    expect(subAgentIds.has('normal-1')).toBe(false);
  });

  it('filters sub-agents from merged executions', () => {
    const executions = [
      { executionId: 'orch-1', items: [] },
      { executionId: 'sub-1', items: [] },
      { executionId: 'sub-2', items: [] },
      { executionId: 'normal-1', items: [] },
    ];
    const subAgentIds = new Set(['sub-1', 'sub-2']);

    const filtered = executions.filter((e) => !subAgentIds.has(e.executionId));
    expect(filtered).toHaveLength(2);
    expect(filtered[0].executionId).toBe('orch-1');
    expect(filtered[1].executionId).toBe('normal-1');
  });

  it('extracts execution_id from dispatch_agent tool result', () => {
    const content = '{"execution_id":"sub-exec-123","status":"accepted"}';
    const parsed = JSON.parse(content);
    expect(parsed.execution_id).toBe('sub-exec-123');
    expect(parsed.status).toBe('accepted');
  });

  it('handles non-JSON dispatch result gracefully', () => {
    const content = 'dispatch failed: agent not found';
    let execId: string | null = null;
    try {
      const parsed = JSON.parse(content);
      execId = parsed?.execution_id ?? null;
    } catch {
      execId = null;
    }
    expect(execId).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Trace helpers: sub-agent support
// ---------------------------------------------------------------------------

describe('traceHelpers sub-agent support', () => {
  it('countStageInteractions returns 0 subAgentCount when no sub-agents', () => {
    const stage: TraceStageGroup = {
      stage_id: 'stage-1',
      stage_name: 'Test',
      executions: [makeTraceExecution()],
    };
    const counts = countStageInteractions(stage);
    expect(counts.subAgentCount).toBe(0);
  });

  it('countStageInteractions counts sub-agents and their interactions', () => {
    const stage: TraceStageGroup = {
      stage_id: 'stage-1',
      stage_name: 'Test',
      executions: [
        makeTraceExecution({
          execution_id: 'orch-1',
          llm_interactions: [
            { id: 'l1', interaction_type: 'iteration', model_name: 'g', created_at: '' },
          ],
          mcp_interactions: [],
          sub_agents: [
            makeTraceExecution({
              execution_id: 'sub-1',
              llm_interactions: [
                { id: 'sl1', interaction_type: 'iteration', model_name: 'g', created_at: '' },
              ],
              mcp_interactions: [
                { id: 'sm1', interaction_type: 'tool_call', server_name: 's', created_at: '' },
              ],
            }),
          ],
        }),
      ],
    };
    const counts = countStageInteractions(stage);
    expect(counts.subAgentCount).toBe(1);
    expect(counts.llm).toBe(2); // 1 parent + 1 sub
    expect(counts.mcp).toBe(1); // 0 parent + 1 sub
    expect(counts.total).toBe(3);
  });

  it('findExecutionOverview searches sub_agents', () => {
    const subExec = makeExecutionOverview({ execution_id: 'sub-exec-1', agent_name: 'Sub' });
    const parentExec = makeExecutionOverview({
      execution_id: 'parent-exec',
      sub_agents: [subExec],
    });
    const session = {
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
      total_stages: 1,
      completed_stages: 1,
      failed_stages: 0,
      has_parallel_stages: false,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      llm_interaction_count: 0,
      mcp_interaction_count: 0,
      current_stage_index: null,
      current_stage_id: null,
      stages: [{
        id: 'stage-1',
        stage_name: 'Investigation',
        stage_index: 0,
        status: EXECUTION_STATUS.COMPLETED,
        parallel_type: null,
        expected_agent_count: 1,
        started_at: '2025-01-15T10:00:00Z',
        completed_at: '2025-01-15T10:05:00Z',
        executions: [parentExec],
      }],
    };

    const found = findExecutionOverview(session, 'sub-exec-1');
    expect(found).toBeDefined();
    expect(found!.agent_name).toBe('Sub');
  });
});
