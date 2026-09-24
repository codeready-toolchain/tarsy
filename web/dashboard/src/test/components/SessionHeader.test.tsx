import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import SessionHeader from '../../components/session/SessionHeader';
import { cancelSession } from '../../services/api';
import { SESSION_STATUS, MAX_CANCEL_REASON_LENGTH } from '../../constants/sessionStatus';
import type { SessionDetailResponse } from '../../types/session';

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
  cancelSession: vi.fn(),
  triggerScoring: vi.fn(),
  handleAPIError: vi.fn((error: unknown) => String(error)),
}));

const mockCancelSession = vi.mocked(cancelSession);

function makeSession(overrides: Partial<SessionDetailResponse> = {}): SessionDetailResponse {
  return {
    id: 'sess-1',
    alert_data: '{}',
    alert_type: 'test-alert',
    status: SESSION_STATUS.IN_PROGRESS,
    chain_id: 'chain-1',
    author: null,
    error_message: null,
    final_analysis: null,
    executive_summary: null,
    executive_summary_error: null,
    labels: null,
    runbook_url: null,
    created_at: '2025-01-15T10:00:00Z',
    started_at: '2025-01-15T10:00:00Z',
    completed_at: null,
    duration_ms: null,
    chat_enabled: false,
    chat_id: null,
    chat_message_count: 0,
    total_stages: 1,
    completed_stages: 0,
    failed_stages: 0,
    has_parallel_stages: false,
    has_action_stages: false,
    actions_executed: null,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    llm_interaction_count: 0,
    mcp_interaction_count: 0,
    current_stage_index: 0,
    current_stage_id: 'stage-1',
    stages: [],
    ...overrides,
  };
}

function renderHeader(session: SessionDetailResponse = makeSession()) {
  return render(
    <MemoryRouter>
      <SessionHeader session={session} />
    </MemoryRouter>,
  );
}

describe('SessionHeader cancel dialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockCancelSession.mockResolvedValue({ session_id: 'sess-1', message: 'cancelling' });
  });

  it('submits without a body when reason is empty', async () => {
    const user = userEvent.setup();
    renderHeader();

    await user.click(screen.getByRole('button', { name: 'Cancel session' }));
    await user.click(screen.getByRole('button', { name: 'CONFIRM CANCELLATION' }));

    expect(mockCancelSession).toHaveBeenCalledWith('sess-1', '');
  });

  it('submits the typed reason', async () => {
    const user = userEvent.setup();
    renderHeader();

    await user.click(screen.getByRole('button', { name: 'Cancel session' }));
    await user.type(screen.getByLabelText(/reason \(optional\)/i), 'duplicate alert');
    await user.click(screen.getByRole('button', { name: 'CONFIRM CANCELLATION' }));

    expect(mockCancelSession).toHaveBeenCalledWith('sess-1', 'duplicate alert');
  });

  it('disables confirm when the reason exceeds 500 runes', async () => {
    const user = userEvent.setup();
    renderHeader();

    await user.click(screen.getByRole('button', { name: 'Cancel session' }));
    const field = screen.getByLabelText(/reason \(optional\)/i);
    await user.click(field);
    field.focus();
    // paste a long string — typing 501 chars is too slow for userEvent
    await user.paste('a'.repeat(MAX_CANCEL_REASON_LENGTH + 1));

    expect(screen.getByRole('button', { name: 'CONFIRM CANCELLATION' })).toBeDisabled();
    expect(mockCancelSession).not.toHaveBeenCalled();
  });
});
