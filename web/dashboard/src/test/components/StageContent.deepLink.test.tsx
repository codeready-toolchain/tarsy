/**
 * Multi-agent deep links must select the owning execution tab (parent tab
 * for nested sub-agent events) so the target TimelineItem can mount.
 */

import { render, screen, waitFor } from '@testing-library/react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import StageContent from '../../components/timeline/StageContent';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import type { ExecutionOverview } from '../../types/session';
import { TIMELINE_STATUS } from '../../constants/eventTypes';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

const theme = createTheme();

function makeItem(overrides: Partial<FlowItem> & { id: string; executionId: string }): FlowItem {
  return {
    type: FLOW_ITEM.THINKING,
    stageId: 'stage-1',
    content: `content-${overrides.id}`,
    status: TIMELINE_STATUS.COMPLETED,
    timestamp: '2025-01-15T10:00:00Z',
    sequenceNumber: 1,
    ...overrides,
  };
}

function makeExecOverview(
  executionId: string,
  agentName: string,
  agentIndex: number,
  subAgents?: ExecutionOverview[],
): ExecutionOverview {
  return {
    execution_id: executionId,
    agent_name: agentName,
    agent_index: agentIndex,
    status: EXECUTION_STATUS.COMPLETED,
    llm_backend: 'test',
    llm_provider: 'test',
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:05:00Z',
    duration_ms: 300000,
    error_message: null,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    sub_agents: subAgents,
  };
}

describe('StageContent deep-link tab selection', () => {
  it('selects the execution tab that owns the deep-linked event', async () => {
    const items: FlowItem[] = [
      makeItem({ id: 'event-a', executionId: 'exec-a', sequenceNumber: 1, content: 'agent-a thought' }),
      makeItem({ id: 'event-b', executionId: 'exec-b', sequenceNumber: 2, content: 'agent-b thought' }),
    ];

    render(
      <ThemeProvider theme={theme}>
        <StageContent
          items={items}
          stageId="stage-1"
          executionOverviews={[
            makeExecOverview('exec-a', 'Agent A', 1),
            makeExecOverview('exec-b', 'Agent B', 2),
          ]}
          forceExpandedItemId="event-b"
          sessionId="session-1"
        />
      </ThemeProvider>,
    );

    await waitFor(() => {
      const panel = document.getElementById('reasoning-tabpanel-1');
      expect(panel).not.toBeNull();
      expect(panel).not.toHaveAttribute('hidden');
      expect(panel?.querySelector('[data-flow-item-id="event-b"]')).not.toBeNull();
    });

    // Owning agent card is labeled and present.
    expect(screen.getByText('Agent B')).toBeInTheDocument();
  });

  it('selects the parent execution tab for a nested sub-agent event', async () => {
    const items: FlowItem[] = [
      makeItem({ id: 'event-a', executionId: 'exec-a', sequenceNumber: 1, content: 'parent-a' }),
      makeItem({ id: 'event-b', executionId: 'exec-b', sequenceNumber: 2, content: 'parent-b' }),
      makeItem({
        id: 'event-sub',
        executionId: 'exec-sub',
        parentExecutionId: 'exec-b',
        sequenceNumber: 3,
        content: 'sub-agent thought',
      }),
    ];

    render(
      <ThemeProvider theme={theme}>
        <StageContent
          items={items}
          stageId="stage-1"
          executionOverviews={[
            makeExecOverview('exec-a', 'Agent A', 1),
            makeExecOverview('exec-b', 'Agent B', 2, [
              makeExecOverview('exec-sub', 'Sub Agent', 1),
            ]),
          ]}
          forceExpandedItemId="event-sub"
          sessionId="session-1"
        />
      </ThemeProvider>,
    );

    await waitFor(() => {
      const panel = document.getElementById('reasoning-tabpanel-1');
      expect(panel).not.toBeNull();
      expect(panel).not.toHaveAttribute('hidden');
    });

    expect(screen.getByText('Agent B')).toBeInTheDocument();
  });
});
