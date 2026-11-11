/**
 * Post-investigation stages finish after the session is terminal. A terminal
 * stage.status for one must re-fetch the session (per-stage actions_executed)
 * and the timeline (memo with the YES/NO marker stripped).
 */

import { act, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import type { SessionDetailResponse, StageOverview, TimelineEvent } from '../../types/session';
import {
  EVENT_STAGE_STATUS,
  STAGE_TYPE,
  TIMELINE_EVENT_TYPES,
  TIMELINE_STATUS,
} from '../../constants/eventTypes';
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

let sessionHandler: ((data: Record<string, unknown>) => void) | null = null;
vi.mock('../../services/websocket.ts', () => ({
  websocketService: {
    connect: () => {},
    subscribeToChannel: (_channel: string, handler: (data: Record<string, unknown>) => void) => {
      sessionHandler = handler;
      return () => {
        sessionHandler = null;
      };
    },
  },
}));

// Stable callbacks, like the real hook: new identities would re-run the
// page's WebSocket effect and cancel its pending timeline re-fetch.
const chatState = {
  sendingMessage: false,
  canceling: false,
  chatStageId: null,
  error: null,
  sendMessage: vi.fn(),
  cancelExecution: vi.fn(),
  onStageStarted: vi.fn(),
  onStageTerminal: vi.fn(),
  clearError: vi.fn(),
};
vi.mock('../../hooks/useChatState.ts', () => ({
  useChatState: () => chatState,
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
  default: () => <div data-testid="conversation-timeline" />,
}));

import { getSession, getTimeline } from '../../services/api';
import { PageHeaderProvider } from '../../contexts/PageHeaderContext';
import { SessionDetailPage } from '../../pages/SessionDetailPage';

const mockGetSession = vi.mocked(getSession);
const mockGetTimeline = vi.mocked(getTimeline);

const SESSION_ID = 'session-pi';
const PI_STAGE_ID = 'stage-pi';

function piStage(status: string, actionsExecuted: boolean | null): StageOverview {
  return {
    id: PI_STAGE_ID,
    stage_name: 'file-ticket',
    stage_index: 2,
    stage_type: STAGE_TYPE.POST_INVESTIGATION,
    status,
    parallel_type: null,
    expected_agent_count: 1,
    started_at: '2025-01-15T10:06:00Z',
    completed_at: null,
    actions_executed: actionsExecuted,
  };
}

function makeSession(stage: StageOverview): SessionDetailResponse {
  return {
    id: SESSION_ID,
    alert_data: '{}',
    alert_type: 'test',
    status: SESSION_STATUS.COMPLETED,
    chain_id: 'chain-1',
    author: null,
    error_message: null,
    final_analysis: 'done',
    executive_summary: null,
    executive_summary_error: null,
    labels: null,
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
    thinking_tokens: 0,
    cache_read_tokens: 0,
    cache_creation_tokens: 0,
    llm_interaction_count: 0,
    mcp_interaction_count: 0,
    current_stage_index: null,
    current_stage_id: null,
    stages: [stage],
  };
}

function memoEvent(content: string): TimelineEvent {
  return {
    id: 'event-memo',
    session_id: SESSION_ID,
    stage_id: PI_STAGE_ID,
    execution_id: 'exec-pi',
    sequence_number: 5,
    event_type: TIMELINE_EVENT_TYPES.FINAL_ANALYSIS,
    status: TIMELINE_STATUS.COMPLETED,
    content,
    metadata: null,
    created_at: '2025-01-15T10:07:00Z',
    updated_at: '2025-01-15T10:07:00Z',
  };
}

function renderPage() {
  const router = createMemoryRouter(
    [{ path: '/sessions/:id', element: <SessionDetailPage /> }],
    { initialEntries: [`/sessions/${SESSION_ID}`] },
  );
  render(
    <PageHeaderProvider>
      <RouterProvider router={router} />
    </PageHeaderProvider>,
  );
}

function stageStatusEvent(stageType: string, stageId: string, status: string) {
  return {
    type: EVENT_STAGE_STATUS,
    session_id: SESSION_ID,
    stage_id: stageId,
    stage_name: 'file-ticket',
    stage_index: 3,
    stage_type: stageType,
    status,
    timestamp: '2025-01-15T10:07:00Z',
  };
}

describe('SessionDetailPage post-investigation live updates', () => {
  beforeEach(() => {
    mockGetSession.mockReset();
    mockGetTimeline.mockReset();
    mockGetSession.mockResolvedValue(makeSession(piStage(EXECUTION_STATUS.ACTIVE, null)));
    mockGetTimeline.mockResolvedValue([]);
  });

  it('re-fetches session and timeline when a post-investigation stage finishes', async () => {
    renderPage();

    const card = await screen.findByTestId('post-investigation-card');
    expect(card).toHaveTextContent('Running…');
    expect(mockGetSession).toHaveBeenCalledTimes(1);
    expect(mockGetTimeline).toHaveBeenCalledTimes(1);

    mockGetSession.mockResolvedValue(makeSession(piStage(EXECUTION_STATUS.COMPLETED, true)));
    mockGetTimeline.mockResolvedValue([memoEvent('Filed a ticket for the owning team.')]);

    await act(async () => {
      sessionHandler?.(stageStatusEvent(STAGE_TYPE.POST_INVESTIGATION, PI_STAGE_ID, EXECUTION_STATUS.COMPLETED));
    });

    await waitFor(() => {
      expect(screen.getByTestId('post-investigation-card')).toHaveTextContent('Acted');
      expect(screen.getByTestId('post-investigation-card')).toHaveTextContent('Filed a ticket for the owning team.');
    });
    expect(mockGetSession).toHaveBeenCalledTimes(2);
    expect(mockGetTimeline).toHaveBeenCalledTimes(2);
  });

  it('does not re-fetch for a non-terminal post-investigation stage event', async () => {
    renderPage();
    await screen.findByTestId('post-investigation-card');

    await act(async () => {
      sessionHandler?.(stageStatusEvent(STAGE_TYPE.POST_INVESTIGATION, PI_STAGE_ID, EXECUTION_STATUS.ACTIVE));
    });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 400));
    });

    expect(mockGetSession).toHaveBeenCalledTimes(1);
    expect(mockGetTimeline).toHaveBeenCalledTimes(1);
  });
});
