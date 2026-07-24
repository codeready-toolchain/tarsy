import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import type { UsageSummaryResponse } from '../../types/api';

vi.mock('../../services/api.ts', () => ({
  getUsageSummary: vi.fn(),
  getFilterOptions: vi.fn(),
  handleAPIError: (err: unknown) =>
    err instanceof Error ? err.message : 'An unexpected error occurred',
}));

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

vi.mock('../../contexts/AuthContext.tsx', () => ({
  useAuth: () => ({
    authAvailable: false,
    isAuthenticated: false,
    user: null,
  }),
}));

vi.mock('../../contexts/VersionContext.tsx', () => ({
  useVersion: () => ({
    currentVersion: 'test',
    updateAvailable: false,
  }),
}));

import { getFilterOptions, getUsageSummary } from '../../services/api';
import { UsagePage } from '../../pages/UsagePage';

const mockGetUsageSummary = vi.mocked(getUsageSummary);
const mockGetFilterOptions = vi.mocked(getFilterOptions);

function makeSummary(overrides: Partial<UsageSummaryResponse> = {}): UsageSummaryResponse {
  return {
    cost_estimation_enabled: true,
    window: {
      start: '2026-06-23T00:00:00.000Z',
      end: '2026-07-23T00:00:00.000Z',
    },
    rank_by: 'cost',
    totals: {
      input_tokens: 100,
      output_tokens: 50,
      total_tokens: 150,
      estimated_cost_usd: 1.23,
      cost_completeness: 'complete',
      unpriced_interaction_count: 0,
    },
    by_model: [
      {
        model_name: 'gemini-flash',
        input_tokens: 100,
        output_tokens: 50,
        total_tokens: 150,
        estimated_cost_usd: 1.23,
        priced: true,
      },
    ],
    by_alert_type: [{ alert_type: 'kubernetes', total_tokens: 150, estimated_cost_usd: 1.23 }],
    by_chain: [{ chain_id: 'default', total_tokens: 150, estimated_cost_usd: 1.23 }],
    top_sessions: [
      {
        session_id: 'abcdef12-3456-7890-abcd-ef1234567890',
        alert_type: 'kubernetes',
        chain_id: 'default',
        total_tokens: 150,
        estimated_cost_usd: 1.23,
        cost_completeness: 'complete',
        created_at: '2026-07-01T12:00:00Z',
      },
    ],
    ...overrides,
  };
}

function renderUsagePage() {
  return render(
    <MemoryRouter>
      <UsagePage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetFilterOptions.mockResolvedValue({
    alert_types: ['kubernetes'],
    chain_ids: ['default'],
    statuses: [],
  });
});

describe('UsagePage', () => {
  it('fetches summary with default 30d window on load', async () => {
    mockGetUsageSummary.mockResolvedValue(makeSummary());

    renderUsagePage();

    await waitFor(() => {
      expect(mockGetUsageSummary).toHaveBeenCalled();
    });

    const params = mockGetUsageSummary.mock.calls[0][0];
    expect(params.start_date).toBeTruthy();
    expect(params.end_date).toBeTruthy();
    expect(params.rank_by).toBeUndefined();

    const start = new Date(params.start_date).getTime();
    const end = new Date(params.end_date).getTime();
    const dayMs = 24 * 60 * 60 * 1000;
    expect(end - start).toBeGreaterThan(29 * dayMs);
    expect(end - start).toBeLessThan(31 * dayMs);

    expect(await screen.findByText('Totals')).toBeInTheDocument();
    expect(screen.getAllByText('Est. $1.23').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: 'Select time range' })).toHaveTextContent(
      'Last 30 days',
    );
  });

  it('re-fetches when rank_by changes', async () => {
    const user = userEvent.setup();
    mockGetUsageSummary.mockResolvedValue(makeSummary());

    renderUsagePage();
    await screen.findByText('Totals');

    const select = screen.getByLabelText('Rank top sessions');
    await user.click(select);
    await user.click(await screen.findByRole('option', { name: 'By tokens' }));

    await waitFor(() => {
      expect(mockGetUsageSummary).toHaveBeenCalledTimes(2);
    });
    expect(mockGetUsageSummary.mock.calls[1][0].rank_by).toBe('tokens');
  });

  it('hides cost columns when estimation is disabled', async () => {
    mockGetUsageSummary.mockResolvedValue(
      makeSummary({
        cost_estimation_enabled: false,
        rank_by: 'tokens',
        totals: {
          input_tokens: 100,
          output_tokens: 50,
          total_tokens: 150,
        },
        by_model: [
          {
            model_name: 'gemini-flash',
            input_tokens: 100,
            output_tokens: 50,
            total_tokens: 150,
          },
        ],
        by_alert_type: [{ alert_type: 'kubernetes', total_tokens: 150 }],
        by_chain: [{ chain_id: 'default', total_tokens: 150 }],
        top_sessions: [
          {
            session_id: 'abcdef12-3456-7890-abcd-ef1234567890',
            alert_type: 'kubernetes',
            chain_id: 'default',
            total_tokens: 150,
            created_at: '2026-07-01T12:00:00Z',
          },
        ],
      }),
    );

    renderUsagePage();
    await screen.findByText('Totals');

    expect(screen.queryByText(/Est\./)).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Rank top sessions')).not.toBeInTheDocument();

    const modelSection = screen.getByText('By model').closest('.MuiPaper-root');
    expect(modelSection).toBeInstanceOf(HTMLElement);
    expect(within(modelSection as HTMLElement).queryByText('Est. cost')).not.toBeInTheDocument();
    expect(within(modelSection as HTMLElement).getByText('Tokens')).toBeInTheDocument();
  });
});
