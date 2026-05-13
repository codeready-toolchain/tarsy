import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReviewActivityItem, ReviewActivityResponse } from '../../types/api';

vi.mock('../../services/api.ts', () => ({
  getReviewActivity: vi.fn(),
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

import { getReviewActivity } from '../../services/api';
import ReviewActivityCard from '../../components/session/ReviewActivityCard';

const mockGetReviewActivity = vi.mocked(getReviewActivity);

const claimActivity: ReviewActivityItem = {
  id: 'act-1',
  actor: 'alice',
  action: 'claim',
  from_status: 'needs_review',
  to_status: 'in_progress',
  created_at: new Date(Date.now() - 60_000).toISOString(),
};

const acknowledgeActivity: ReviewActivityItem = {
  id: 'act-2',
  actor: 'bob',
  action: 'acknowledge',
  from_status: 'in_progress',
  to_status: 'reviewed',
  created_at: new Date(Date.now() - 30_000).toISOString(),
};

const completeActivity: ReviewActivityItem = {
  id: 'act-3',
  actor: 'alice',
  action: 'complete',
  from_status: 'in_progress',
  to_status: 'reviewed',
  quality_rating: 'accurate',
  note: 'Restarted the pod.',
  investigation_feedback: 'Root cause was OOM.',
  created_at: new Date(Date.now() - 10_000).toISOString(),
};

const updateFeedbackActivity: ReviewActivityItem = {
  id: 'act-4',
  actor: 'carol',
  action: 'update_feedback',
  from_status: 'reviewed',
  to_status: 'reviewed',
  quality_rating: 'partially_accurate',
  note: 'Updated action taken.',
  created_at: new Date(Date.now() - 5_000).toISOString(),
};

function mockResponse(activities: ReviewActivityItem[]): ReviewActivityResponse {
  return { activities };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('ReviewActivityCard', () => {
  it('renders nothing when no activities are returned', async () => {
    mockGetReviewActivity.mockResolvedValue(mockResponse([]));

    const { container } = render(
      <ReviewActivityCard sessionId="sess-1" />,
    );

    await waitFor(() => {
      expect(mockGetReviewActivity).toHaveBeenCalledWith('sess-1');
      expect(container.firstChild).toBeNull();
    });
  });

  it('shows header with count badge and expands on click', async () => {
    mockGetReviewActivity.mockResolvedValue(
      mockResponse([claimActivity, acknowledgeActivity]),
    );

    render(<ReviewActivityCard sessionId="sess-1" />);

    await waitFor(() => {
      expect(screen.getByText('Review Activity')).toBeInTheDocument();
    });

    expect(screen.getByText('2')).toBeInTheDocument();

    const header = screen.getByText('Review Activity');
    await userEvent.click(header);

    expect(screen.getByText('Claimed')).toBeInTheDocument();
    expect(screen.getByText('Acknowledged')).toBeInTheDocument();
  });

  it('renders acknowledge row with neutral styling and no feedback', async () => {
    mockGetReviewActivity.mockResolvedValue(
      mockResponse([acknowledgeActivity]),
    );

    render(<ReviewActivityCard sessionId="sess-1" />);

    await waitFor(() => {
      expect(screen.getByText('Review Activity')).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText('Review Activity'));

    expect(screen.getByText('Acknowledged')).toBeInTheDocument();
    expect(screen.getByText('by bob')).toBeInTheDocument();

    // Acknowledge should NOT render note or feedback
    expect(screen.queryByText('Restarted the pod.')).not.toBeInTheDocument();
    expect(screen.queryByText('Root cause was OOM.')).not.toBeInTheDocument();
  });

  it('renders complete row with rating chip, note, and feedback', async () => {
    mockGetReviewActivity.mockResolvedValue(
      mockResponse([completeActivity]),
    );

    render(<ReviewActivityCard sessionId="sess-1" />);

    await waitFor(() => {
      expect(screen.getByText('Review Activity')).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText('Review Activity'));

    expect(screen.getByText('Completed Review')).toBeInTheDocument();
    expect(screen.getByText('by alice')).toBeInTheDocument();
    expect(screen.getByText('Accurate')).toBeInTheDocument();
    expect(screen.getByText('Restarted the pod.')).toBeInTheDocument();
    expect(screen.getByText('Root cause was OOM.')).toBeInTheDocument();
  });

  it('renders update_feedback row with rating chip and note', async () => {
    mockGetReviewActivity.mockResolvedValue(
      mockResponse([updateFeedbackActivity]),
    );

    render(<ReviewActivityCard sessionId="sess-1" />);

    await waitFor(() => {
      expect(screen.getByText('Review Activity')).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText('Review Activity'));

    expect(screen.getByText('Updated Feedback')).toBeInTheDocument();
    expect(screen.getByText('by carol')).toBeInTheDocument();
    expect(screen.getByText('Partially Accurate')).toBeInTheDocument();
    expect(screen.getByText('Updated action taken.')).toBeInTheDocument();
  });

  it('refetches when refreshCounter changes', async () => {
    mockGetReviewActivity.mockResolvedValue(mockResponse([claimActivity]));

    const { rerender } = render(
      <ReviewActivityCard sessionId="sess-1" refreshCounter={0} />,
    );

    await waitFor(() => {
      expect(mockGetReviewActivity).toHaveBeenCalledTimes(1);
    });

    mockGetReviewActivity.mockResolvedValue(
      mockResponse([claimActivity, acknowledgeActivity]),
    );

    rerender(<ReviewActivityCard sessionId="sess-1" refreshCounter={1} />);

    await waitFor(() => {
      expect(mockGetReviewActivity).toHaveBeenCalledTimes(2);
    });
  });

  it('shows error alert with retry on fetch failure', async () => {
    mockGetReviewActivity.mockRejectedValue(new Error('Network error'));

    render(<ReviewActivityCard sessionId="sess-1" />);

    await waitFor(() => {
      expect(screen.getByText('Failed to load review activity.')).toBeInTheDocument();
    });

    mockGetReviewActivity.mockResolvedValue(mockResponse([claimActivity]));
    await userEvent.click(screen.getByText('Retry'));

    await waitFor(() => {
      expect(screen.getByText('Review Activity')).toBeInTheDocument();
    });
  });
});
