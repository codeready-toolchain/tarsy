import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CompleteReviewModal } from '../../components/dashboard/CompleteReviewModal';
import { QUALITY_RATING, REVIEW_SELECTION } from '../../types/api';

vi.mock('../../config/env.ts', () => ({
  config: { isDevelopment: false, isProduction: true },
  DASHBOARD_VERSION: 'test',
  urls: {
    api: { base: '', health: '/health' },
    websocket: { base: 'ws://localhost:8080', path: '/api/v1/ws' },
    oauth: { signIn: '/oauth2/sign_in', signOut: '/oauth2/sign_out', userInfo: '/oauth2/userinfo' },
  },
}));

const defaultProps = {
  open: true,
  onClose: vi.fn(),
  onComplete: vi.fn(),
};

describe('CompleteReviewModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders all four radio options including Acknowledge', () => {
    render(<CompleteReviewModal {...defaultProps} />);

    expect(screen.getByText('Accurate')).toBeInTheDocument();
    expect(screen.getByText('Partially Accurate')).toBeInTheDocument();
    expect(screen.getByText('Inaccurate')).toBeInTheDocument();
    expect(screen.getByText('Acknowledge')).toBeInTheDocument();
  });

  it('shows feedback fields when a quality rating is selected', () => {
    render(<CompleteReviewModal {...defaultProps} />);

    expect(screen.getByLabelText(/action taken/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/investigation feedback/i)).toBeInTheDocument();
  });

  it('hides feedback fields when Acknowledge is selected', async () => {
    const user = userEvent.setup();
    render(<CompleteReviewModal {...defaultProps} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    const actionField = screen.getByLabelText(/action taken/i);
    const collapseWrapper = actionField.closest('.MuiCollapse-root');
    await waitFor(() => {
      expect(collapseWrapper).toHaveStyle({ height: '0px' });
    });
  });

  it('shows "Complete Review" button for quality ratings', () => {
    render(<CompleteReviewModal {...defaultProps} />);

    expect(screen.getByRole('button', { name: 'Complete Review' })).toBeInTheDocument();
  });

  it('shows "Acknowledge" button when Acknowledge is selected', async () => {
    const user = userEvent.setup();
    render(<CompleteReviewModal {...defaultProps} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    expect(screen.getByRole('button', { name: 'Acknowledge' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Complete Review' })).not.toBeInTheDocument();
  });

  it('calls onComplete with acknowledge value when submitted', async () => {
    const user = userEvent.setup();
    const onComplete = vi.fn();
    render(<CompleteReviewModal {...defaultProps} onComplete={onComplete} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    await user.click(ackRadio);

    const submitBtn = screen.getByRole('button', { name: 'Acknowledge' });
    await user.click(submitBtn);

    expect(onComplete).toHaveBeenCalledWith(REVIEW_SELECTION.ACKNOWLEDGE, undefined, undefined);
  });

  it('calls onComplete with quality rating and feedback when submitted', async () => {
    const user = userEvent.setup();
    const onComplete = vi.fn();
    render(<CompleteReviewModal {...defaultProps} onComplete={onComplete} />);

    const actionInput = screen.getByLabelText(/action taken/i);
    await user.type(actionInput, 'Restarted the pod');

    const submitBtn = screen.getByRole('button', { name: 'Complete Review' });
    await user.click(submitBtn);

    expect(onComplete).toHaveBeenCalledWith(QUALITY_RATING.ACCURATE, 'Restarted the pod', undefined);
  });

  it('pre-selects acknowledge when initialRating is acknowledge', () => {
    render(<CompleteReviewModal {...defaultProps} initialRating={REVIEW_SELECTION.ACKNOWLEDGE} />);

    const ackRadio = screen.getByRole('radio', { name: /acknowledge/i });
    expect(ackRadio).toBeChecked();
    expect(screen.getByRole('button', { name: 'Acknowledge' })).toBeInTheDocument();
  });
});
