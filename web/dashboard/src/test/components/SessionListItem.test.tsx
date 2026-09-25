import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { SessionListItem } from '../../components/dashboard/SessionListItem';
import type { DashboardSessionItem } from '../../types/session';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

function makeSession(overrides: Partial<DashboardSessionItem> = {}): DashboardSessionItem {
  return {
    id: 'sess-1',
    alert_type: 'pod-crash',
    chain_id: 'chain-1',
    status: 'completed',
    author: null,
    created_at: '2025-01-15T10:00:00Z',
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:05:00Z',
    duration_ms: 300000,
    error_message: null,
    executive_summary: null,
    labels: null,
    llm_interaction_count: 1,
    mcp_interaction_count: 0,
    input_tokens: 100,
    output_tokens: 30,
    total_tokens: 130,
    thinking_tokens: 0,
    cache_read_tokens: 0,
    cache_creation_tokens: 0,
    total_stages: 1,
    completed_stages: 1,
    has_parallel_stages: false,
    has_sub_agents: false,
    has_action_stages: false,
    actions_executed: null,
    chat_message_count: 0,
    provider_fallback_count: 0,
    current_stage_index: null,
    current_stage_id: null,
    matched_in_content: false,
    ...overrides,
  };
}

function renderItem(session: DashboardSessionItem) {
  return render(
    <MemoryRouter>
      <table>
        <tbody>
          <SessionListItem session={session} searchTerm="" />
        </tbody>
      </table>
    </MemoryRouter>,
  );
}

describe('SessionListItem token tooltip', () => {
  it('shows cache and thinking in the tooltip when they are non-zero', async () => {
    const user = userEvent.setup();
    renderItem(makeSession({
      input_tokens: 100,
      output_tokens: 20,
      total_tokens: 200,
      cache_read_tokens: 40,
      cache_creation_tokens: 10,
      thinking_tokens: 30,
    }));

    await user.hover(screen.getByText('100'));

    const tooltip = await screen.findByRole('tooltip');
    expect(tooltip).toHaveTextContent('total');
    expect(tooltip).toHaveTextContent('200');
    expect(tooltip).toHaveTextContent('in');
    expect(tooltip).toHaveTextContent('100');
    expect(tooltip).toHaveTextContent('out');
    expect(tooltip).toHaveTextContent('20');
    expect(tooltip).toHaveTextContent('cache read');
    expect(tooltip).toHaveTextContent('40');
    expect(tooltip).toHaveTextContent('cache create');
    expect(tooltip).toHaveTextContent('10');
    expect(tooltip).toHaveTextContent('thinking');
    expect(tooltip).toHaveTextContent('30');
  });

  it('omits zero cache and thinking lines', async () => {
    const user = userEvent.setup();
    renderItem(makeSession());

    await user.hover(screen.getByText('100'));

    const tooltip = await screen.findByRole('tooltip');
    expect(tooltip).toHaveTextContent('total');
    expect(tooltip).toHaveTextContent('130');
    expect(tooltip).not.toHaveTextContent('cache read');
    expect(tooltip).not.toHaveTextContent('cache create');
    expect(tooltip).not.toHaveTextContent('thinking');
  });
});
