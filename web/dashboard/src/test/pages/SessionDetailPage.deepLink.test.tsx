/**
 * Deep-link navigation between sessions must wait for the loaded session
 * to match the route id before resolving query params.
 */

import { render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import type { SessionDetailResponse, TimelineEvent } from '../../types/session';
import { TIMELINE_EVENT_TYPES, TIMELINE_STATUS } from '../../constants/eventTypes';
import { EXECUTION_STATUS, SESSION_STATUS } from '../../constants/sessionStatus';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

vi.mock('../../services/api.ts', () => ({
  getSession: vi.fn(),
  getTimeline: vi.fn(),
  updateReview: vi.fn(),
  handleAPIError: (err: unknown) =>
    err instanceof Error ? err.message : 'An unexpected error occurred',
}));

vi.mock('../../services/websocket.ts', () => ({
  websocketService: {
    subscribeToChannel: () => () => {},
    connect: () => {},
  },
}));

vi.mock('../../hooks/useChatState.ts', () => ({
  useChatState: () => ({
    sendingMessage: false,
    canceling: false,
    chatStageId: null,
    error: null,
    sendMessage: vi.fn(),
    cancelExecution: vi.fn(),
    onStageStarted: vi.fn(),
    onStageTerminal: vi.fn(),
    clearError: vi.fn(),
  }),
}));

vi.mock('../../hooks/useAdvancedAutoScroll.ts', () => ({
  useAdvancedAutoScroll: () => ({
    containerRef: { current: null },
    scrollToBottom: vi.fn(),
    isNearBottom: true,
  }),
}));

vi.mock('../../components/layout/VersionFooter.tsx', () => ({
  VersionFooter: () => null,
}));

vi.mock('../../components/common/FloatingSubmitAlertFab.tsx', () => ({
  FloatingSubmitAlertFab: () => null,
}));

vi.mock('../../components/session/SessionHeader.tsx', () => ({
  default: () => <div data-testid="session-header" />,
}));

vi.mock('../../components/session/FinalAnalysisCard.tsx', () => ({
  default: () => <div data-testid="final-analysis" />,
}));

vi.mock('../../components/session/ReviewActivityCard.tsx', () => ({
  default: () => null,
}));

vi.mock('../../components/session/ExtractedLearningsCard.tsx', () => ({
  default: () => null,
}));

vi.mock('../../components/chat/ChatPanel.tsx', () => ({
  default: () => null,
}));

vi.mock('../../components/session/ConversationTimeline.tsx', () => ({
  default: function MockTimeline(props: {
    focusRequest?: { stageId: string; eventId?: string; nonce: number };
  }) {
    return (
      <div
        data-testid="conversation-timeline"
        data-focus-stage={props.focusRequest?.stageId ?? ''}
        data-focus-event={props.focusRequest?.eventId ?? ''}
        data-focus-nonce={String(props.focusRequest?.nonce ?? '')}
      />
    );
  },
}));

import { getSession, getTimeline } from '../../services/api';
import { PageHeaderProvider } from '../../contexts/PageHeaderContext';
import { SessionDetailPage } from '../../pages/SessionDetailPage';

const mockGetSession = vi.mocked(getSession);
const mockGetTimeline = vi.mocked(getTimeline);

function makeSession(
  id: string,
  stageId: string,
  overrides: Partial<SessionDetailResponse> = {},
): SessionDetailResponse {
  return {
    id,
    alert_data: '{}',
    alert_type: 'test',
    status: SESSION_STATUS.COMPLETED,
    chain_id: 'chain-1',
    author: null,
    error_message: null,
    final_analysis: 'done',
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
    has_action_stages: false,
    actions_executed: null,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    llm_interaction_count: 0,
    mcp_interaction_count: 0,
    current_stage_index: null,
    current_stage_id: null,
    stages: [
      {
        id: stageId,
        stage_name: 'Investigation',
        stage_index: 0,
        stage_type: 'investigation',
        status: EXECUTION_STATUS.COMPLETED,
        parallel_type: null,
        expected_agent_count: 1,
        started_at: '2025-01-15T10:00:00Z',
        completed_at: '2025-01-15T10:05:00Z',
        executions: [
          {
            execution_id: `exec-${stageId}`,
            agent_name: 'agent',
            agent_index: 1,
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
          },
        ],
      },
    ],
    ...overrides,
  };
}

function makeTimeline(sessionId: string, stageId: string, eventId: string): TimelineEvent[] {
  return [
    {
      id: eventId,
      session_id: sessionId,
      stage_id: stageId,
      execution_id: `exec-${stageId}`,
      sequence_number: 1,
      event_type: TIMELINE_EVENT_TYPES.LLM_THINKING,
      status: TIMELINE_STATUS.COMPLETED,
      content: `thinking for ${sessionId}`,
      metadata: null,
      created_at: '2025-01-15T10:00:00Z',
      updated_at: '2025-01-15T10:00:05Z',
    },
  ];
}

function renderAt(path: string) {
  const router = createMemoryRouter(
    [{ path: '/sessions/:id', element: <SessionDetailPage /> }],
    { initialEntries: [path] },
  );
  render(
    <PageHeaderProvider>
      <RouterProvider router={router} />
    </PageHeaderProvider>,
  );
  return router;
}

describe('SessionDetailPage deep-link navigation', () => {
  beforeEach(() => {
    mockGetSession.mockReset();
    mockGetTimeline.mockReset();

    mockGetSession.mockImplementation(async (id: string) => {
      if (id === 'session-a') return makeSession('session-a', 'stage-a');
      if (id === 'session-b') return makeSession('session-b', 'stage-b');
      throw new Error(`unknown session ${id}`);
    });
    mockGetTimeline.mockImplementation(async (id: string) => {
      if (id === 'session-a') return makeTimeline('session-a', 'stage-a', 'event-a');
      if (id === 'session-b') return makeTimeline('session-b', 'stage-b', 'event-b');
      throw new Error(`unknown timeline ${id}`);
    });
  });

  it('resolves destination session query params after navigating between deep links', async () => {
    const router = renderAt('/sessions/session-a?stage=stage-a&event=event-a');

    const timeline = await screen.findByTestId('conversation-timeline');
    await waitFor(() => {
      expect(timeline).toHaveAttribute('data-focus-stage', 'stage-a');
      expect(timeline).toHaveAttribute('data-focus-event', 'event-a');
    });

    await router.navigate('/sessions/session-b?stage=stage-b&event=event-b');

    await waitFor(() => {
      const next = screen.getByTestId('conversation-timeline');
      expect(next).toHaveAttribute('data-focus-stage', 'stage-b');
      expect(next).toHaveAttribute('data-focus-event', 'event-b');
    });

    // Destination session data was loaded (not resolved against stale session-a).
    expect(mockGetSession).toHaveBeenCalledWith('session-b');
    expect(mockGetTimeline).toHaveBeenCalledWith('session-b');
  });
});
