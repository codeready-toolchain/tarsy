import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ReviewCell } from '../../components/dashboard/ReviewCell';
import { REVIEW_STATUS } from '../../types/api';
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
    status: 'completed',
    alert_type: 'test-alert',
    chain_id: 'chain-1',
    review_status: null,
    quality_rating: null,
    action_taken: null,
    investigation_feedback: null,
    assignee: null,
    feedback_edited: false,
    feedback_edited_by: null,
    feedback_edited_at: null,
    executive_summary: null,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    has_parallel_stages: false,
    latest_score: null,
    scoring_status: null,
    ...overrides,
  } as DashboardSessionItem;
}

function renderInTable(ui: React.ReactElement) {
  return render(
    <table>
      <tbody>
        <tr>{ui}</tr>
      </tbody>
    </table>,
  );
}

describe('ReviewCell', () => {
  it('renders nothing for non-terminal sessions without review', () => {
    const session = makeSession({ status: 'running' });
    const { container } = renderInTable(<ReviewCell session={session} />);
    const cell = container.querySelector('td');
    expect(cell?.querySelector('.MuiChip-root')).toBeNull();
  });

  it('renders ghost chip on hover for unreviewed terminal sessions', () => {
    const session = makeSession({ status: 'completed' });
    const onReviewClick = vi.fn();
    renderInTable(<ReviewCell session={session} onReviewClick={onReviewClick} />);

    const chip = screen.getByRole('button');
    expect(chip).toHaveStyle({ opacity: '0' });
  });

  it('renders clickable acknowledged chip for acknowledged terminal sessions', async () => {
    const user = userEvent.setup();
    const session = makeSession({
      status: 'completed',
      review_status: REVIEW_STATUS.REVIEWED,
      quality_rating: null,
    });
    const onReviewClick = vi.fn();
    renderInTable(<ReviewCell session={session} onReviewClick={onReviewClick} />);

    const chip = screen.getByRole('button');
    expect(chip).toHaveAttribute('tabindex', '0');

    await user.click(chip);
    expect(onReviewClick).toHaveBeenCalledWith(session);
  });

  it('renders non-interactive acknowledged chip when no onReviewClick', () => {
    const session = makeSession({
      status: 'completed',
      review_status: REVIEW_STATUS.REVIEWED,
      quality_rating: null,
    });
    renderInTable(<ReviewCell session={session} />);

    const chip = screen.getByLabelText(/acknowledged/i);
    expect(chip).toHaveAttribute('tabindex', '-1');
  });

  it('renders rated chip that is clickable', async () => {
    const user = userEvent.setup();
    const session = makeSession({
      status: 'completed',
      review_status: REVIEW_STATUS.REVIEWED,
      quality_rating: 'accurate',
    });
    const onReviewClick = vi.fn();
    renderInTable(<ReviewCell session={session} onReviewClick={onReviewClick} />);

    const chip = screen.getByRole('button');
    await user.click(chip);
    expect(onReviewClick).toHaveBeenCalledWith(session);
  });
});
