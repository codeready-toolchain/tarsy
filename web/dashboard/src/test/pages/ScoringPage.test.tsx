import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import type { SessionScoreResponse } from '../../types/api';
import { PageHeaderProvider } from '../../contexts/PageHeaderContext';

vi.mock('../../services/api.ts', () => ({
  getSession: vi.fn(),
  getScore: vi.fn(),
  triggerScoring: vi.fn(),
  handleAPIError: (err: unknown) =>
    err instanceof Error ? err.message : 'An unexpected error occurred',
}));

vi.mock('../../components/layout/VersionFooter.tsx', () => ({
  VersionFooter: () => null,
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

import { getScore, getSession, triggerScoring } from '../../services/api';
import { ScoringPage } from '../../pages/ScoringPage';

const mockGetSession = vi.mocked(getSession);
const mockGetScore = vi.mocked(getScore);
const mockTriggerScoring = vi.mocked(triggerScoring);

const failedScore: SessionScoreResponse = {
  score_id: 'score-1',
  total_score: null,
  score_analysis: 'Previous analysis',
  tool_improvement_report: null,
  failure_tags: null,
  prompt_hash: null,
  score_triggered_by: 'auto',
  status: 'failed',
  stage_id: 'stage-1',
  started_at: '2026-09-25T12:00:00.000Z',
  completed_at: '2026-09-25T12:01:00.000Z',
  error_message: 'Scoring interrupted',
};

const inProgressScore: SessionScoreResponse = {
  ...failedScore,
  score_id: 'score-2',
  score_analysis: null,
  status: 'in_progress',
  completed_at: null,
  error_message: null,
  started_at: '2026-09-25T12:05:00.000Z',
};

function renderPage() {
  return render(
    <PageHeaderProvider>
      <MemoryRouter initialEntries={['/sessions/session-1/scoring']}>
        <Routes>
          <Route path="/sessions/:id/scoring" element={<ScoringPage />} />
        </Routes>
      </MemoryRouter>
    </PageHeaderProvider>,
  );
}

describe('ScoringPage re-score', () => {
  beforeEach(() => {
    sessionHandler = null;
    mockGetSession.mockReset();
    mockGetScore.mockReset();
    mockTriggerScoring.mockReset();
    mockGetSession.mockResolvedValue({ id: 'session-1', status: 'completed' } as never);
    mockTriggerScoring.mockResolvedValue({ score_id: 'score-2' });
  });

  it('shows the scoring spinner as soon as re-score is confirmed', async () => {
    let releaseRefresh: (score: SessionScoreResponse) => void = () => {};
    mockGetScore
      .mockResolvedValueOnce(failedScore)
      .mockImplementationOnce(
        () => new Promise((resolve) => {
          releaseRefresh = resolve;
        }),
      );

    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole('button', { name: 'Click to re-score' }));
    await user.click(screen.getByRole('button', { name: 'Confirm Re-score' }));

    expect(await screen.findByText('Scoring')).toBeInTheDocument();
    expect(screen.queryByText('Scoring interrupted')).not.toBeInTheDocument();
    expect(screen.queryByText('Previous analysis')).not.toBeInTheDocument();

    releaseRefresh(inProgressScore);
    await waitFor(() => {
      expect(mockGetScore).toHaveBeenCalledTimes(2);
    });
    expect(screen.getByText('Scoring')).toBeInTheDocument();
    expect(screen.queryByText('Scoring interrupted')).not.toBeInTheDocument();
  });

  it('shows the scoring spinner when a score-updated event reports in progress', async () => {
    mockGetScore.mockResolvedValue(failedScore);
    renderPage();

    await screen.findByRole('button', { name: 'Click to re-score' });
    sessionHandler?.({
      type: 'session.score_updated',
      session_id: 'session-1',
      scoring_status: 'in_progress',
      timestamp: '2026-09-25T12:05:00.000Z',
    });

    expect(await screen.findByText('Scoring')).toBeInTheDocument();
    expect(screen.queryByText('Scoring interrupted')).not.toBeInTheDocument();
  });
});
